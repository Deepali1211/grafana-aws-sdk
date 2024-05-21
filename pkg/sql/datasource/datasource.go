package datasource

import (
	"database/sql"
	"fmt"
	"sync"

	"github.com/grafana/grafana-aws-sdk/pkg/awsds"
	"github.com/grafana/grafana-aws-sdk/pkg/sql/api"
	"github.com/grafana/grafana-aws-sdk/pkg/sql/driver"
	asyncDriver "github.com/grafana/grafana-aws-sdk/pkg/sql/driver/async"
	"github.com/grafana/grafana-aws-sdk/pkg/sql/models"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/sqlds/v3"
)

// AWSDatasource stores a cache of several instances.
// Each Map will depend on the datasource ID (and connection options):
//   - sessionCache: AWS cache. This is not a Map since it does not depend on the datasource.
//   - config: Base configuration. It will be used as base to populate datasource settings.
//     It does not depend on connection options (only one per datasource)
//   - api: API instance with the common methods to contact the data source API.
type AWSDatasource struct {
	sessionCache *awsds.SessionCache
	config       sync.Map
	api          sync.Map
}

func New() *AWSDatasource {
	ds := &AWSDatasource{sessionCache: awsds.NewSessionCache()}
	return ds
}

func (ds *AWSDatasource) storeConfig(config backend.DataSourceInstanceSettings) {
	ds.config.Store(config.ID, config)
}

func (ds *AWSDatasource) createDB(dr driver.Driver) (*sql.DB, error) {
	db, err := dr.OpenDB()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to connect to database (check hostname and port?)", err)
	}

	return db, nil
}

func (ds *AWSDatasource) createAsyncDB(dr asyncDriver.Driver) (awsds.AsyncDB, error) {
	db, err := dr.GetAsyncDB()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to connect to database (check hostname and port)", err)
	}

	return db, nil
}

func (ds *AWSDatasource) storeAPI(id int64, args sqlds.Options, dsAPI api.AWSAPI) {
	key := connectionKey(id, args)
	ds.api.Store(key, dsAPI)
}

func (ds *AWSDatasource) loadAPI(id int64, args sqlds.Options) (api.AWSAPI, bool) {
	key := connectionKey(id, args)
	dsAPI, exists := ds.api.Load(key)
	if exists {
		return dsAPI.(api.AWSAPI), true
	}
	return nil, false
}

func (ds *AWSDatasource) createAPI(id int64, args sqlds.Options, settings models.Settings, loader api.Loader) (api.AWSAPI, error) {
	dsAPI, err := loader(ds.sessionCache, settings)
	if err != nil {
		return nil, fmt.Errorf("%w: Failed to create client", err)
	}
	ds.storeAPI(id, args, dsAPI)
	return dsAPI, err
}

func (ds *AWSDatasource) createDriver(dsAPI api.AWSAPI, loader driver.Loader) (driver.Driver, error) {
	dr, err := loader(dsAPI)
	if err != nil {
		return nil, fmt.Errorf("%w: Failed to create client", err)
	}

	return dr, nil
}

func (ds *AWSDatasource) createAsyncDriver(dsAPI api.AWSAPI, loader asyncDriver.Loader) (asyncDriver.Driver, error) {
	dr, err := loader(dsAPI)
	if err != nil {
		return nil, fmt.Errorf("%w: Failed to create client", err)
	}

	return dr, nil
}

func (ds *AWSDatasource) parseSettings(id int64, args sqlds.Options, settings models.Settings) error {
	config, ok := ds.config.Load(id)
	if !ok {
		return fmt.Errorf("unable to find stored configuration for datasource %d. Initialize it first", id)
	}
	err := settings.Load(config.(backend.DataSourceInstanceSettings))
	if err != nil {
		return fmt.Errorf("error reading settings: %s", err.Error())
	}
	settings.Apply(args)
	return nil
}

// Init stores the data source configuration. It's needed for the GetDB and GetAPI functions
func (ds *AWSDatasource) Init(config backend.DataSourceInstanceSettings) {
	ds.storeConfig(config)
}

// GetDB returns a *sql.DB. It will use the loader functions to initialize the required
// settings, API and driver and finally create a DB.
func (ds *AWSDatasource) GetDB(
	id int64,
	options sqlds.Options,
	settingsLoader models.Loader,
	apiLoader api.Loader,
	driverLoader driver.Loader,
) (*sql.DB, error) {
	settings := settingsLoader()
	err := ds.parseSettings(id, options, settings)
	if err != nil {
		return nil, err
	}

	dsAPI, err := ds.createAPI(id, options, settings, apiLoader)
	if err != nil {
		return nil, err
	}

	dr, err := ds.createDriver(dsAPI, driverLoader)
	if err != nil {
		return nil, err
	}

	return ds.createDB(dr)
}

// GetAsyncDB returns a sqlds.AsyncDB. It will use the loader functions to initialize the required
// settings, API and driver and finally create a DB.
func (ds *AWSDatasource) GetAsyncDB(
	id int64,
	options sqlds.Options,
	settingsLoader models.Loader,
	apiLoader api.Loader,
	driverLoader asyncDriver.Loader,
) (awsds.AsyncDB, error) {
	settings := settingsLoader()
	err := ds.parseSettings(id, options, settings)
	if err != nil {
		return nil, err
	}

	dsAPI, err := ds.createAPI(id, options, settings, apiLoader)
	if err != nil {
		return nil, err
	}

	dr, err := ds.createAsyncDriver(dsAPI, driverLoader)
	if err != nil {
		return nil, err
	}

	return ds.createAsyncDB(dr)
}

// GetAPI returns an API interface. When called multiple times with the same id and options, it
// will return a cached version of the API. The first time, it will use the loader
// functions to initialize the required settings and API.
func (ds *AWSDatasource) GetAPI(
	id int64,
	options sqlds.Options,
	settingsLoader models.Loader,
	apiLoader api.Loader,
) (api.AWSAPI, error) {
	cachedAPI, exists := ds.loadAPI(id, options)
	if exists {
		return cachedAPI, nil
	}

	// create new api
	settings := settingsLoader()
	err := ds.parseSettings(id, options, settings)
	if err != nil {
		return nil, err
	}
	return ds.createAPI(id, options, settings, apiLoader)
}
