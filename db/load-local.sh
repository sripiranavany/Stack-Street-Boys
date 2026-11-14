#!/bin/sh
# Local docker-compose only: load the City Pulse CSVs (same steps as db/README.md "Load").
set -e
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\copy sensors(sensor_id,name,longitude,latitude,first_seen,last_seen,is_test) FROM /data/sensors_load.csv CSV HEADER"
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\copy readings(time,sensor_id,type,value) FROM /data/readings_load.csv CSV HEADER"
