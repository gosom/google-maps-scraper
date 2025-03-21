-- Run this script as a database superuser to set up the database and permissions correctly
-- Example: psql -U postgres -f scripts/setup_db.sql

-- Create database if it doesn't exist
SELECT 'CREATE DATABASE google_maps_scraper' 
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'google_maps_scraper')
\gexec

-- Connect to the database
\c google_maps_scraper

-- Create scraper user if it doesn't exist
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'scraper') THEN
        CREATE USER scraper WITH PASSWORD 'strongpassword';
    END IF;
END $$;

-- Grant schema permissions
GRANT ALL PRIVILEGES ON SCHEMA public TO scraper;
ALTER SCHEMA public OWNER TO scraper;

-- Clean existing migrations table if it exists (let golang-migrate create a fresh one)
DROP TABLE IF EXISTS schema_migrations;

-- Grant permissions on future objects
ALTER DEFAULT PRIVILEGES IN SCHEMA public 
GRANT ALL PRIVILEGES ON TABLES TO scraper;

ALTER DEFAULT PRIVILEGES IN SCHEMA public 
GRANT ALL PRIVILEGES ON SEQUENCES TO scraper;

ALTER DEFAULT PRIVILEGES IN SCHEMA public 
GRANT ALL PRIVILEGES ON FUNCTIONS TO scraper;

-- Now the database is set up for the scraper user to run migrations and use the application
SELECT 'Database setup complete. You can now run the application with:'
\gexec
SELECT './google-maps-scraper -web -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable"'
\gexec