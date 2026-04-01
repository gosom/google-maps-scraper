-- =============================================================================
-- Database Setup Script
-- =============================================================================
--
-- Usage:
--   psql -h localhost -p 5432 -U postgres \
--     -v db_name=mydb \
--     -v db_user=myuser \
--     -v db_pass=mypassword \
--     -f scripts/setup_db.sql
--
-- Example (local development):
--   psql -h localhost -p 5432 -U postgres \
--     -v db_name=google_maps_scraper \
--     -v db_user=scraper \
--     -v db_pass=changeme123 \
--     -f scripts/setup_db.sql
--
-- Example (remote server):
--   psql -h db.example.com -p 5432 -U postgres \
--     -v db_name=brezel_prod \
--     -v db_user=brezel \
--     -v "db_pass=S3cur3P@ss!" \
--     -f scripts/setup_db.sql
--
-- After running, start the app with:
--   ./brezel-api -web -dsn "postgres://<db_user>:<db_pass>@localhost:5432/<db_name>?sslmode=disable"
-- =============================================================================

-- Validate that all required variables are set
\if :{?db_name}
\else
  \echo 'ERROR: db_name is not set. Pass it with: -v db_name=yourdb'
  \quit
\endif

\if :{?db_user}
\else
  \echo 'ERROR: db_user is not set. Pass it with: -v db_user=youruser'
  \quit
\endif

\if :{?db_pass}
\else
  \echo 'ERROR: db_pass is not set. Pass it with: -v db_pass=yourpassword'
  \quit
\endif

-- Create database if it doesn't exist
SELECT 'CREATE DATABASE ' || :'db_name'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name')
\gexec

-- Connect to the new database
\c :db_name

-- Create or update user (uses \gexec to evaluate psql variables then execute)
SELECT format('DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = %L) THEN CREATE USER %I WITH PASSWORD %L; ELSE ALTER USER %I WITH PASSWORD %L; END IF; END $$;', :'db_user', :'db_user', :'db_pass', :'db_user', :'db_pass')
\gexec

-- Grant schema permissions
SELECT format('GRANT ALL PRIVILEGES ON SCHEMA public TO %I', :'db_user') \gexec
SELECT format('ALTER SCHEMA public OWNER TO %I', :'db_user') \gexec

-- Install required extensions (superuser only)
CREATE EXTENSION IF NOT EXISTS pgcrypto;  -- for gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS btree_gist; -- for exclusion constraints

-- Grant extension function access
SELECT format('GRANT USAGE ON SCHEMA public TO %I', :'db_user') \gexec
SELECT format('GRANT EXECUTE ON FUNCTION gen_random_uuid() TO %I', :'db_user') \gexec

-- Clean existing migrations table (let golang-migrate create a fresh one)
DROP TABLE IF EXISTS schema_migrations;

-- Grant permissions on future objects
SELECT format('ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON TABLES TO %I', :'db_user') \gexec
SELECT format('ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON SEQUENCES TO %I', :'db_user') \gexec
SELECT format('ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON FUNCTIONS TO %I', :'db_user') \gexec

-- Done
\echo ''
\echo 'Database setup complete!'
\echo ''
\echo 'Start the application with:'
SELECT format('  ./brezel-api -web -dsn "postgres://%s:%s@localhost:5432/%s?sslmode=disable"', :'db_user', :'db_pass', :'db_name') AS "";
\echo ''
