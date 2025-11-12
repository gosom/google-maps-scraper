-- This migration ensures the scraper user has proper permissions for the schema
-- It should be run by a superuser or database owner, but the migrations will try to run it anyway

-- Grant schema creation and usage permissions to scraper user
-- Note: These might fail if the migration user doesn't have permissions, which is expected
DO $$
BEGIN
    -- Try to grant privileges, but ignore errors
    BEGIN
        EXECUTE 'GRANT ALL PRIVILEGES ON SCHEMA public TO scraper';
        EXCEPTION WHEN OTHERS THEN
            -- Ignore the error
    END;
    
    BEGIN
        EXECUTE 'ALTER SCHEMA public OWNER TO scraper';
        EXCEPTION WHEN OTHERS THEN
            -- Ignore the error
    END;
END
$$;