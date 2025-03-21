# Database Setup Guide

This document explains how to set up the PostgreSQL database for the Google Maps Scraper.

## Prerequisites

- PostgreSQL server installed and running
- PostgreSQL client tools (`psql`)
- Superuser access to the PostgreSQL server (for initial setup)

## Database Migration System

The application uses [golang-migrate](https://github.com/golang-migrate/migrate) to manage database schema migrations. All database changes are tracked in migration files located in `scripts/migrations/` with the format `NNNNNN_description.(up|down).sql`.

This ensures:
1. All database changes are properly versioned
2. Changes can be applied and rolled back consistently
3. The database schema matches the application expectations

## Setup Process

### Option 1: Automatic Setup (Recommended)

1. Run the setup script as a database superuser (e.g., postgres):

```bash
psql -U postgres -f scripts/setup_db.sql
```

This will:
- Create the `google_maps_scraper` database if it doesn't exist
- Create the `scraper` user with the password `strongpassword`
- Grant necessary permissions to the `scraper` user
- Set up proper ownership of database objects

2. Once the setup script completes, you can run the application:

```bash
./google-maps-scraper -web -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable"
```

The application will automatically run all migrations during startup.

### Option 2: Manual Setup

If you prefer to set up the database manually, follow these steps:

1. Connect to PostgreSQL as a superuser:

```bash
psql -U postgres
```

2. Create the database and user:

```sql
CREATE DATABASE google_maps_scraper;
CREATE USER scraper WITH PASSWORD 'strongpassword';
```

3. Connect to the new database:

```sql
\c google_maps_scraper
```

4. Grant necessary permissions:

```sql
GRANT ALL PRIVILEGES ON SCHEMA public TO scraper;
ALTER SCHEMA public OWNER TO scraper;

-- Grant permissions on future objects
ALTER DEFAULT PRIVILEGES IN SCHEMA public 
GRANT ALL PRIVILEGES ON TABLES TO scraper;

ALTER DEFAULT PRIVILEGES IN SCHEMA public 
GRANT ALL PRIVILEGES ON SEQUENCES TO scraper;

ALTER DEFAULT PRIVILEGES IN SCHEMA public 
GRANT ALL PRIVILEGES ON FUNCTIONS TO scraper;
```

5. Exit psql:

```sql
\q
```

6. Run the application:

```bash
./google-maps-scraper -web -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable"
```

## Troubleshooting

If you encounter permission errors when running migrations, the most likely cause is that the `scraper` user doesn't have sufficient permissions on the database schema.

Common errors and solutions:

1. "ERROR: permission denied for schema public":
   - The `scraper` user needs ownership of the public schema
   - Run `ALTER SCHEMA public OWNER TO scraper;` as a superuser

2. "Failed to create migration instance":
   - The application can't create or access the migrations directory
   - Check that the migrations directory exists and the application has read permissions

3. "ERROR: permission denied for sequence user_usage_id_seq":
   - Run `GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO scraper;` as a superuser

## Adding New Migrations

When making database changes:

1. Create a new migration file with a sequential number:
   - `NNNNNN_descriptive_name.up.sql` for the changes
   - `NNNNNN_descriptive_name.down.sql` for reverting the changes

2. All migration files must have both "up" and "down" versions to be valid

3. The migrations should be idempotent (can be applied multiple times safely)
   - Use `IF NOT EXISTS` and `IF EXISTS` clauses
   - Check for existing constraints before adding them

4. Remember to include appropriate permissions:
   - `GRANT ALL PRIVILEGES ON TABLE new_table TO scraper;`
   - `GRANT ALL PRIVILEGES ON SEQUENCE new_sequence TO scraper;`

## Custom Configuration

If you want to use a different database name, user, or password, make the appropriate changes in the setup script and when launching the application.

Remember to update the DSN string with your custom settings:

```
postgres://USERNAME:PASSWORD@HOST:PORT/DATABASE?sslmode=disable
```