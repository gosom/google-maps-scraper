# Brezel.ai Staging Deployment Guide

This guide provides step-by-step instructions for deploying the Google Maps Scraper API on a Linux staging server using Docker and external PostgreSQL.

## Prerequisites

Before deploying, ensure your Linux server has:

- **Docker** and **Docker Compose** installed
- **PostgreSQL** running (can be on the same server or external)
- **Git** for cloning the repository
- **Sufficient resources**: Minimum 2GB RAM, 2 CPU cores (recommended: 4GB RAM, 4 CPU cores)

## Quick Start

### 1. Clone and Build

```bash
# Clone the repository
git clone <your-repository-url>
cd google-maps-scraper-2

# Build the Docker image
docker build -t brezel-staging-test .
```

### 2. Configure Environment

Create your environment configuration:

```bash
# Copy the example env file
cp .env.staging.example .env.staging

# Edit with your database credentials
nano .env.staging
```

### 3. Deploy

```bash
# Start the service
docker compose -f docker-compose.staging.yaml --env-file .env.staging up -d

# Verify deployment
curl http://localhost:8080/health
```

## Detailed Setup

### Step 1: Server Prerequisites

#### Install Docker (Ubuntu/Debian)
```bash
# Update package index
sudo apt update

# Install prerequisites
sudo apt install -y apt-transport-https ca-certificates curl software-properties-common

# Add Docker's official GPG key
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

# Set up the stable repository
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# Install Docker Engine
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# Add your user to docker group
sudo usermod -aG docker $USER
newgrp docker
```

#### Install PostgreSQL (if not already available)
```bash
# Install PostgreSQL
sudo apt install -y postgresql postgresql-contrib

# Start and enable PostgreSQL
sudo systemctl start postgresql
sudo systemctl enable postgresql

# Create database and user
sudo -u postgres psql -c "CREATE DATABASE google_maps_scraper;"
sudo -u postgres psql -c "CREATE USER scraper WITH ENCRYPTED PASSWORD 'your_strong_password';"
sudo -u postgres psql -c "GRANT ALL PRIVILEGES ON DATABASE google_maps_scraper TO scraper;"
sudo -u postgres psql -c "ALTER USER scraper CREATEDB;"
```

### Step 2: Prepare the Application

#### Clone Repository
```bash
# Clone the repository
git clone <your-repository-url>
cd google-maps-scraper-2

# Make sure you're on the correct branch
git checkout develop  # or your preferred branch
```

#### Build Docker Image
```bash
# Build the staging image
docker build -t brezel-staging-test .

# Verify the image was built
docker images | grep brezel-staging-test
```

### Step 3: Configure Environment Variables

#### Create Environment File
```bash
# Copy the example file
cp .env.staging.example .env.staging

# Edit the environment file
nano .env.staging
```

#### Environment Configuration for Linux Servers

For **Linux servers**, update your `.env.staging` file:

```bash
# Database Configuration
# Replace with your actual database credentials
DSN=postgres://scraper:your_password@172.17.0.1:5432/google_maps_scraper?sslmode=disable

# Application Configuration
WEB_ADDR=:8080
DATA_FOLDER=/gmapsdata
LOG_LEVEL=info

# Optional configurations
DISABLE_TELEMETRY=0
CLERK_SECRET_KEY=
```

**Important Notes:**
- For Linux Docker, use `172.17.0.1` (default Docker bridge IP) instead of `host.docker.internal`
- If PostgreSQL is on a different server, use that server's IP address
- Use `sslmode=require` for production environments

### Step 4: Deploy the Application

#### Start the Service
```bash
# Start the application
docker compose -f docker-compose.staging.yaml --env-file .env.staging up -d

# Check if it's running
docker ps
```

#### Verify Deployment
```bash
# Check health endpoint
curl http://localhost:8080/health

# Check API status
curl http://localhost:8080/api/v1/status

# Check logs
docker logs google-maps-scraper-2-brezel-api-1
```

### Step 5: Configure Firewall (if needed)

```bash
# Allow HTTP traffic (if using UFW)
sudo ufw allow 8080/tcp

# Or for iptables
sudo iptables -A INPUT -p tcp --dport 8080 -j ACCEPT
sudo iptables-save
```

## Production Considerations

### Security

1. **Database Security**
   ```bash
   # Use strong passwords
   DSN=postgres://scraper:$(openssl rand -base64 32)@172.17.0.1:5432/google_maps_scraper?sslmode=require
   ```

2. **Firewall Configuration**
   ```bash
   # Restrict database access
   sudo ufw allow from 172.17.0.0/16 to any port 5432
   ```

3. **SSL/TLS**
   - Use a reverse proxy (nginx) with SSL certificates
   - Enable SSL for PostgreSQL connections

### Monitoring

#### Health Checks
```bash
# Add to crontab for monitoring
*/5 * * * * curl -f http://localhost:8080/health || echo "Service down" | mail -s "Alert" admin@example.com
```

#### Log Management
```bash
# Configure log rotation
docker compose -f docker-compose.staging.yaml --env-file .env.staging logs --follow

# Or use external log management
docker logs google-maps-scraper-2-brezel-api-1 2>&1 | logger -t brezel-api
```

### Backup Strategy

```bash
# Database backup
pg_dump -h 172.17.0.1 -U scraper google_maps_scraper > backup-$(date +%Y%m%d).sql

# Data folder backup
tar -czf gmapsdata-backup-$(date +%Y%m%d).tar.gz ./gmapsdata/
```

## Troubleshooting

### Common Issues

#### 1. Database Connection Failed
```bash
# Check if PostgreSQL is running
sudo systemctl status postgresql

# Test connection manually
psql -h 172.17.0.1 -U scraper -d google_maps_scraper

# Check Docker network
docker network ls
docker network inspect bridge
```

#### 2. Container Won't Start
```bash
# Check logs
docker logs google-maps-scraper-2-brezel-api-1

# Check environment variables
docker exec google-maps-scraper-2-brezel-api-1 env | grep DSN
```

#### 3. Port Already in Use
```bash
# Find what's using port 8080
sudo netstat -tulpn | grep :8080

# Stop conflicting services
sudo systemctl stop service-name
```

#### 4. Migration Issues
```bash
# Check migration status
docker exec google-maps-scraper-2-brezel-api-1 ls -la /scripts/migrations/

# Run migrations manually if needed
docker exec google-maps-scraper-2-brezel-api-1 brezel-api -dsn "$DSN" -produce
```

### Useful Commands

```bash
# Restart the service
docker compose -f docker-compose.staging.yaml --env-file .env.staging restart

# Update the application
git pull
docker build -t brezel-staging-test .
docker compose -f docker-compose.staging.yaml --env-file .env.staging up -d

# Clean up old images
docker image prune -f

# Monitor resource usage
docker stats google-maps-scraper-2-brezel-api-1
```

## API Endpoints

Once deployed, your API will be available at:

- **Health Check**: `http://your-server:8080/health`
- **API Status**: `http://your-server:8080/api/v1/status`
- **Web Interface**: `http://your-server:8080/`
- **API Documentation**: `http://your-server:8080/api/docs`

## Scaling

For high-traffic environments:

1. **Horizontal Scaling**: Run multiple container instances
2. **Load Balancing**: Use nginx or HAProxy
3. **Database Scaling**: Use PostgreSQL read replicas
4. **Resource Limits**: Configure Docker resource constraints

```yaml
# Add to docker-compose.staging.yaml
services:
  brezel-api:
    # ... other config
    deploy:
      resources:
        limits:
          memory: 2G
          cpus: '1.0'
```

## Support

For issues and questions:
- Check the logs first: `docker logs google-maps-scraper-2-brezel-api-1`
- Review this deployment guide
- Check the main README.md for application-specific help