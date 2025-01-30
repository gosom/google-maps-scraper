# Vector Leads Scraper - Technical Design Document

## Overview
The Vector Leads Scraper is a sophisticated system designed for scraping business information from Google Maps at scale. It employs a distributed job processing architecture with PostgreSQL as the primary data store, enabling concurrent processing across multiple instances.

## Core Components

### 1. Job Processing Architecture

#### 1.1 Job Types
The system supports three main types of jobs:
- **Search Jobs (`GmapJob`)**: Initial jobs that search Google Maps for business listings
- **Place Jobs (`PlaceJob`)**: Extract detailed information from individual business pages
- **Email Extract Jobs (`EmailExtractJob`)**: Optional jobs to extract email addresses from business websites

#### 1.2 Job States
Jobs can exist in the following states:
- `new`: Newly created job
- `queued`: Job picked up for processing
- `working`: Currently being processed
- `ok`: Successfully completed
- `failed`: Failed to process

### 2. Database Provider (`postgres.provider`)

The database provider serves as the central job coordination mechanism with the following responsibilities:

#### 2.1 Core Functions
1. **Job Queue Management**
   ```go
   func (p *provider) Jobs(ctx context.Context) (<-chan scrapemate.IJob, <-chan error)
   ```
   - Manages job distribution using PostgreSQL's SKIP LOCKED feature
   - Implements batch processing with configurable batch sizes
   - Provides automatic job retry mechanisms

2. **Job Storage**
   ```go
   func (p *provider) Push(ctx context.Context, job scrapemate.IJob) error
   ```
   - Stores jobs in the `gmaps_jobs` table
   - Handles job serialization/deserialization
   - Manages job priorities and scheduling

#### 2.2 Database Schema
```sql
CREATE TABLE gmaps_jobs (
    id TEXT PRIMARY KEY,
    priority INTEGER,
    payload_type TEXT,
    payload BYTEA,
    created_at TIMESTAMP,
    status TEXT
);

CREATE TABLE results (
    id SERIAL PRIMARY KEY,
    data JSONB
);
```

### 3. Result Processing

#### 3.1 Result Writer
The `postgres.resultWriter` handles storing scraped data:
- Implements batch processing (50 entries per batch)
- Automatic batch flushing every minute
- Transaction-based writes for data consistency
- JSON storage for flexible schema evolution

```go
func (r *resultWriter) Run(ctx context.Context, in <-chan scrapemate.Result) error
```

### 4. Scaling and Performance

#### 4.1 Performance Metrics
- Processing speed: ~120 jobs/minute with c=8, depth=1
- Batch size: 50 results per write operation
- Maximum database connections: 10 per instance

#### 4.2 Scaling Capabilities
- Horizontal scaling through multiple scraper instances
- Kubernetes deployment support
- Built-in job deduplication
- Configurable concurrency levels

## Implementation Details

### 1. Database Schema Evolution

#### 1.1 Core Tables
The system uses two primary tables that evolved through several migrations:

1. **gmaps_jobs Table**
```sql
CREATE TABLE gmaps_jobs(
    id UUID PRIMARY KEY,
    priority SMALLINT NOT NULL,
    payload_type TEXT NOT NULL,
    payload BYTEA NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    status TEXT NOT NULL
);
```

2. **results Table**
Final schema after evolution:
```sql
CREATE TABLE results(
    id INT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    data JSONB NOT NULL
);
```

#### 1.2 Schema Evolution Path
1. Initial Schema:
   - Structured columns for specific fields (title, category, address, etc.)
2. Geographic Enhancement:
   - Added latitude/longitude columns for spatial data
3. Flexible Schema:
   - Migrated to JSONB for dynamic data structure
   - Enables storage of varying business attributes
   - Supports future field additions without schema changes

### 2. Database Interactions

#### 2.1 Job Queue Management
```go
func (p *provider) fetchJobs(ctx context.Context) {
    // Uses PostgreSQL's SKIP LOCKED for concurrent job processing
    q := `
    WITH updated AS (
        UPDATE gmaps_jobs
        SET status = $1
        WHERE id IN (
            SELECT id from gmaps_jobs
            WHERE status = $2
            ORDER BY priority ASC, created_at ASC 
            FOR UPDATE SKIP LOCKED 
            LIMIT $3
        )
        RETURNING *
    )
    SELECT payload_type, payload from updated 
    ORDER by priority ASC, created_at ASC
    `
}
```

#### 2.2 Result Storage
```go
type resultWriter struct {
    db *sql.DB
}

func (r *resultWriter) batchSave(ctx context.Context, entries []*gmaps.Entry) error {
    // Batch insert with transaction support
    // Handles up to 50 entries per batch
    // Automatic flush every minute
}
```

### 3. Data Flow Optimizations

#### 3.1 Batch Processing
- **Write Batching**:
  ```go
  const maxBatchSize = 50
  buff := make([]*gmaps.Entry, 0, 50)
  ```
  - Accumulates up to 50 results before writing
  - Forces flush on minute intervals
  - Uses transaction for atomic writes

#### 3.2 Concurrency Control
```go
type provider struct {
    db        *sql.DB
    mu        *sync.Mutex
    jobc      chan scrapemate.IJob
    batchSize int
}
```
- Mutex-protected job channel
- Configurable batch sizes
- Connection pooling (max 10 connections)

### 4. Data Storage Patterns

#### 4.1 JSONB Storage Strategy
```json
{
    "title": "Business Name",
    "category": "Business Category",
    "address": "Full Address",
    "coordinates": {
        "latitude": 123.456,
        "longitude": 789.012
    },
    "contact": {
        "website": "https://example.com",
        "phone": "+1234567890",
        "email": "contact@example.com"
    },
    "metrics": {
        "review_count": 100,
        "rating": 4.5
    },
    "metadata": {
        "scraped_at": "2024-03-21T12:00:00Z",
        "source_url": "https://maps.google.com/..."
    }
}
```

#### 4.2 Indexing Strategy
```sql
CREATE INDEX idx_results_data_gin ON results USING gin (data);
CREATE INDEX idx_gmaps_jobs_status ON gmaps_jobs(status);
CREATE INDEX idx_gmaps_jobs_priority_created ON gmaps_jobs(priority, created_at);
```

### 5. Error Handling and Recovery

#### 5.1 Transaction Management
```go
tx, err := r.db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer func() {
    _ = tx.Rollback()
}()
```

#### 5.2 Retry Mechanisms
- Exponential backoff for failed jobs
- Automatic job requeuing on failure
- Transaction rollback on partial failures

### 6. Performance Considerations

#### 6.1 Write Optimization
- Batch inserts for improved throughput
- JSONB compression for storage efficiency
- Periodic VACUUM for space reclamation

#### 6.2 Read Optimization
- Index usage for job queue queries
- JSONB path operators for efficient data access
- Connection pooling for resource management

## Deployment and Operations

### 1. Local Development
```bash
docker-compose -f docker-compose.dev.yaml up -d
```

### 2. Production Deployment
- Kubernetes-ready with Helm charts
- Configurable through environment variables
- Supports multiple scraper instances
- Built-in monitoring and telemetry

### 3. Configuration Options
- Concurrency levels
- Batch sizes
- Database connection parameters
- Proxy support
- Language settings
- Geographical targeting

## Best Practices and Recommendations

1. **Scaling Guidelines**
   - Start with 8 concurrent workers per instance
   - Monitor database connection pool usage
   - Use appropriate batch sizes (default: 50)

2. **Error Handling**
   - Implement automatic retries for failed jobs
   - Use transaction rollbacks for data consistency
   - Monitor error rates and types

3. **Performance Optimization**
   - Use appropriate indexes on database tables
   - Configure batch sizes based on load
   - Monitor and adjust connection pools

4. **Monitoring and Maintenance**
   - Regular database maintenance
   - Monitor job queue lengths
   - Track processing rates and error rates

## Security Considerations

1. **Database Security**
   - Use connection pooling
   - Implement proper authentication
   - Regular security updates

2. **API Security**
   - Rate limiting
   - Request validation
   - Proper error handling

## Conclusion

The Vector Leads Scraper provides a robust, scalable solution for large-scale Google Maps data extraction. Its database-centric architecture enables distributed processing while maintaining data consistency and reliability. The system is designed to be easily deployable, maintainable, and scalable to meet varying workload demands.

This design document should serve as a comprehensive guide for developers looking to understand, use, or contribute to the system. For specific implementation details, refer to the codebase and inline documentation.
