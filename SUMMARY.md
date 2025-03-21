# Summary of PostgreSQL Implementation for Web API

## Files Created/Modified:

1. **postgres/repository.go** - Implements the web.JobRepository interface for PostgreSQL
2. **postgres/migration.go** - Provides migration support for PostgreSQL schema management
3. **postgres/user.go** - Implements user management and usage tracking
4. **web/auth/auth.go** - Adds authentication middleware using Clerk
5. **web/web.go** - Updates to support user isolation and authentication
6. **web/job.go** - Added UserID field for user isolation
7. **web/service.go** - Updated to support user-specific job queries
8. **web/sqlite/sqlite.go** - Updated for backward compatibility with user fields
9. **runner/webrunner/webrunner.go** - Modified to conditionally use PostgreSQL or SQLite

## Migration Files Created:
1. **scripts/migrations/0005_web_jobs_table.up.sql** - Creates the jobs table for the web API
2. **scripts/migrations/0006_users_tables.up.sql** - Adds user management tables
3. **scripts/migrations/0007_schema_migrations.up.sql** - Adds migration tracking table

## Test Files Created:
1. **postgres/repository_test.go** - Tests for the PostgreSQL repository
2. **postgres/user_test.go** - Tests for user management and usage tracking

## Key Features Implemented:

1. **PostgreSQL Repository**
   - Full implementation of web.JobRepository interface
   - Support for JSONB data type for job data
   - Proper connection pooling for production use

2. **Migration System**
   - Automatic schema migration on startup
   - Support for schema versioning
   - Migration tracking to prevent duplicate migrations

3. **User Management**
   - User authentication via Clerk
   - User isolation (users can only see their own jobs)
   - Usage tracking and rate limiting (jobs per day)

4. **Backward Compatibility**
   - SQLite support preserved
   - Automatic fallback if PostgreSQL not configured
   - No breaking changes to existing APIs

## Running the Project

### Setup PostgreSQL Database

1. Install PostgreSQL if you don't have it already
   ```bash
   # macOS with Homebrew
   brew install postgresql@14
   brew services start postgresql@14
   
   # Ubuntu/Debian
   sudo apt-get update
   sudo apt-get install postgresql postgresql-contrib
   sudo systemctl start postgresql
   ```

2. Create a database for the application
   ```bash
   createdb google_maps_scraper
   ```

3. Set up user access (optional)
   ```bash
   psql -c "CREATE USER scraper WITH PASSWORD 'strongpassword';"
   psql -c "GRANT ALL PRIVILEGES ON DATABASE google_maps_scraper TO scraper;"
   ```

### Running the Web API with PostgreSQL

1. Start the web server with PostgreSQL connection:
   ```bash
   ./google-maps-scraper -web -addr ":8080" -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable"
   ```

   If you're using the default PostgreSQL user:
   ```bash
   ./google-maps-scraper -web -addr ":8080" -dsn "postgres://postgres:postgres@localhost:5432/google_maps_scraper?sslmode=disable"
   ```

2. Access the web interface in your browser at http://localhost:8080

3. To enable authentication with Clerk:
   ```bash
   export CLERK_API_KEY="your_clerk_api_key"
   ./google-maps-scraper -web -addr ":8080" -dsn "postgres://postgres:postgres@localhost:5432/google_maps_scraper?sslmode=disable"
   ```

### Testing the API

#### Using the Web Interface
1. Navigate to http://localhost:8080
2. Use the form to create new scraping jobs
3. View job results via the web interface

#### Using the API Endpoints
1. List jobs
   ```bash
   curl http://localhost:8080/api/v1/jobs
   ```

2. Create a new job
   ```bash
   curl -X POST http://localhost:8080/api/v1/jobs \
     -H "Content-Type: application/json" \
     -d '{
       "Name": "Test Job",
       "keywords": ["coffee shop"],
       "lang": "en",
       "zoom": 15,
       "lat": "40.712776",
       "lon": "-74.005974",
       "fast_mode": true,
       "radius": 10000,
       "depth": 5,
       "max_time": 300
     }'
   ```

3. Get a specific job
   ```bash
   curl http://localhost:8080/api/v1/jobs/JOB_ID
   ```

4. Delete a job
   ```bash
   curl -X DELETE http://localhost:8080/api/v1/jobs/JOB_ID
   ```

5. Download job results
   ```bash
   curl -o results.csv http://localhost:8080/api/v1/jobs/JOB_ID/download
   ```

### Running the Tests

1. Set up a test database
   ```bash
   createdb google_maps_scraper_test
   ```

2. Run the PostgreSQL repository tests
   ```bash
   PG_TEST_DSN="postgres://postgres:postgres@localhost:5432/google_maps_scraper_test?sslmode=disable" go test -v ./postgres/...
   ```

3. Run specific test files
   ```bash
   # Test the repository implementation
   PG_TEST_DSN="postgres://postgres:postgres@localhost:5432/google_maps_scraper_test?sslmode=disable" go test -v ./postgres/repository_test.go
   
   # Test the user management
   PG_TEST_DSN="postgres://postgres:postgres@localhost:5432/google_maps_scraper_test?sslmode=disable" go test -v ./postgres/user_test.go
   ```

## Configuration Options

| Flag | Description | Default |
|------|-------------|---------|
| `-web` | Run as web server | `false` |
| `-addr` | Web server address | `:8080` |
| `-dsn` | PostgreSQL connection string | `""` (empty, uses SQLite) |
| `-data-folder` | Data folder for web API | `webdata` |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `CLERK_API_KEY` | Clerk API key for authentication |
| `PG_TEST_DSN` | PostgreSQL connection string for tests |

## Usage Limits:
- Default: 5 jobs per day per user
- Configurable in the code (postgres.NewUsageLimiter)

## Important Notes

1. Authentication is only enabled if both PostgreSQL and a Clerk API key are configured
2. If PostgreSQL is not available, the system will fall back to SQLite
3. Migrations run automatically on startup if using PostgreSQL
4. The web UI will show all jobs, while the API will respect user isolation

## Troubleshooting PostgreSQL Integration

If you encounter issues with PostgreSQL integration, such as "relation does not exist" errors despite successful API responses, follow these steps:

1. **Verify PostgreSQL Connection**
   ```bash
   # Check if you can connect to your PostgreSQL database
   psql "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper"
   
   # Once connected, check if tables exist
   \dt
   ```

2. **Add Debug Flags**
   ```bash
   # Run with debug enabled to see more verbose output
   ./google-maps-scraper -web -addr ":8080" -dsn "postgres://scraper:strongpassword@localhost:5432/google_maps_scraper?sslmode=disable" -debug
   ```

3. **Check Migration Directory**
   Make sure your migrations directory is accessible and contains the required .sql files:
   ```bash
   # Check the migrations directory
   ls -la scripts/migrations/
   ```

4. **Manual Schema Creation**
   If automatic migrations aren't working, you can manually create the schema:
   ```sql
   CREATE TABLE IF NOT EXISTS jobs (
       id TEXT PRIMARY KEY,
       name TEXT NOT NULL,
       status TEXT NOT NULL,
       data JSONB NOT NULL,
       created_at TIMESTAMP NOT NULL DEFAULT NOW(),
       updated_at TIMESTAMP NOT NULL DEFAULT NOW(),
       user_id TEXT
   );
   
   CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status, created_at);
   CREATE INDEX IF NOT EXISTS idx_jobs_user_id ON jobs(user_id);
   ```

5. **Common Issues and Solutions**
   
   - **Multiple Database Connections**: The original implementation was opening multiple database connections, which could cause inconsistency in schema initialization.
   
   - **Migration Path Issues**: The migrations directory path might not be resolved correctly. Try specifying an absolute path to the migrations directory.
   
   - **SQL Statement Execution**: The original implementation tried to execute multiple SQL statements in a single Exec call, which can fail with some PostgreSQL drivers. Make sure to execute each statement separately.
   
   - **Different Table Names**: The web API uses a table named `jobs`, while the database runner uses `gmaps_jobs`. Make sure you're querying the correct table.