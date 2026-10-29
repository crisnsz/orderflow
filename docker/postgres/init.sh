#!/bin/sh
set -e
for db in orders inventory billing payments notifications; do
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres -c "CREATE DATABASE $db"
done
