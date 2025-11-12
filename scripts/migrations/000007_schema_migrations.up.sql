-- This migration is no longer needed as golang-migrate creates its own schema_migrations table
-- This file is kept for compatibility with older systems

-- Ensure all objects have proper permissions
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO scraper;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO scraper;