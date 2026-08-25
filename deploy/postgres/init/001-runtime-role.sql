-- Development-only cluster bootstrap. Production creates the same NOLOGIN
-- capability role and an environment-specific LOGIN out of band, with secrets
-- supplied by the platform secret manager.
CREATE ROLE relay_control_runtime
    NOLOGIN
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION
    NOBYPASSRLS;

CREATE ROLE relay_control_app_dev
    LOGIN
    PASSWORD 'relay_control_runtime_dev_only'
    NOSUPERUSER
    NOCREATEDB
    NOCREATEROLE
    NOREPLICATION
    NOBYPASSRLS
    IN ROLE relay_control_runtime;

GRANT CONNECT ON DATABASE relay_station_control TO relay_control_app_dev;
