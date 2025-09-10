# Decodo Proxy Integration

![Decodo](./img/decodo.png)

## Overview

Decodo offers high-performance proxies with the #1 response time in the market, making them an excellent choice for Google Maps scraping. Their extensive infrastructure and advanced technology ensure reliable data collection without facing common blocking mechanisms.

## Key Features

- **125M+ IP Pool**: Access to one of the largest IP pools in the industry
- **195+ Locations Worldwide**: Global coverage for geo-specific scraping needs
- **#1 Response Time**: Industry-leading performance and speed
- **24/7 Technical Support**: Round-the-clock assistance from experts
- **Extensive Documentation**: Comprehensive guides and API documentation
- **Anti-Detection Technology**: Advanced techniques to bypass CAPTCHAs, IP bans, and geo-restrictions

## Getting Started - Step by Step Guide

Follow these detailed steps to set up Decodo proxies with the Google Maps Scraper:

### Step 1: Sign Up for Decodo

Visit [Decodo's website](https://visit.decodo.com/APVbbx) and create your account to start your 3-day free trial with 100MB.

![Decodo Step 1](./img/decodo-step1.png)

### Step 2: Access Your Dashboard

Once registered, log in to access your Decodo dashboard where you can manage your proxies and view your usage statistics.

![Decodo Step 2](./img/decodo-step2.png)

### Step 3: Get Your Proxy Credentials

Navigate to your proxy settings to obtain your connection details:
- Proxy endpoint (host:port)
- Username and password
- Available proxy locations

![Decodo Step 3](./img/decodo-step3.png)

### Step 4: Configure and Test Your Proxy

Copy your proxy credentials and test the connection. Make note of your proxy format for use with the Google Maps Scraper.

![Decodo Step 4](./img/decodo-step4.png)

### Step 5: Configure Google Maps Scraper

Now use your Decodo proxy credentials with the Google Maps Scraper:

```bash
./google-maps-scraper \
  -input example-queries.txt \
  -results restaurants.csv \
  -proxies 'http://your_username:your_password@your_proxy_endpoint:port' \
  -depth 1 \
  -c 2
```

**Example with actual Decodo proxy format:**
```bash
./google-maps-scraper \
  -input example-queries.txt \
  -results restaurants.csv \
  -proxies 'http://user123:pass456@isp.decodo.com:10001' \
  -depth 1 \
  -c 2
```

## Proxy Configuration Examples

### HTTP Proxy
```bash
-proxies 'http://your_username:your_password@isp.decodo.com:'
```

### HTTPS Proxy
```bash
-proxies 'https://your_username:your_password@proxy.decodo.com:8443'
```

### Multiple Proxies (Load Balancing)
```bash
-proxies 'http://username:password@proxy1.decodo.com:8080,http://username:password@isp.decodo.com:10001'
```

## Complete Setup Example

Here's a complete walkthrough from setup to scraping with Decodo proxies:

### Prerequisites
- Google Maps Scraper installed (either binary or Docker)
- Decodo account with active proxy credentials
- Input file with search queries

### Step-by-Step Execution

1. **Prepare your queries file** (`example-queries.txt`):
```
restaurants in New York
hotels in London  
coffee shops in Tokyo
```

2. **Get your Decodo proxy details** from your dashboard:
- Username: `your_username`
- Password: `your_password` 
- Endpoint: `host:port`

3. **Run the scraper with Decodo proxy**:
```bash
# Basic scraping with Decodo proxy
./google-maps-scraper \
  -input example-queries.txt \
  -results decodo-results.csv \
  -proxies 'http://your_username:your_password@isp.decodo.com:10001' \
  -depth 5 \
  -c 4 \
  -exit-on-inactivity 5m

# With email extraction enabled
./google-maps-scraper \
  -input example-queries.txt \
  -results decodo-results-with-emails.csv \
  -proxies 'http://your_username:your_password@isp.decodo.com:10002' \
  -email \
  -depth 3 \
  -c 2 \
  -exit-on-inactivity 10m

# JSON output format
./google-maps-scraper \
  -input example-queries.txt \
  -results decodo-results.json \
  -proxies 'http://your_username:your_password@isp.decodo.com:10001' \
  -json \
  -depth 3 \
  -c 3
```

4. **Monitor the output**:
```
🌍 Google Maps Scraper
⭐ If you find this project useful, please star it on GitHub
💖 Consider sponsoring to support development

Using proxy: premium.decodo.com:8080
Starting scraper with 4 workers...
Processing: restaurants in New York
Found 20 results, extracting data...
Processing: hotels in London
Found 18 results, extracting data...
...
```

### Docker Example with Decodo

```bash
# Create results file
touch decodo-results.csv

# Run with Docker and Decodo proxy
docker run \
  -v $PWD/example-queries.txt:/example-queries \
  -v $PWD/decodo-results.csv:/results.csv \
  gosom/google-maps-scraper \
  -input /example-queries \
  -results /results.csv \
  -proxies 'http://your_username:your_password@isp.decodo.com:10001' \
  -depth 3 \
  -c 2 \
  -exit-on-inactivity 5m

# Run with Docker and Multiple Decodo proxies
docker run \
  -v $PWD/example-queries.txt:/example-queries \
  -v $PWD/decodo-results.csv:/results.csv \
  gosom/google-maps-scraper \
  -input /example-queries \
  -results /results.csv \
  -proxies 'http://your_username:your_password@isp.decodo.com:10001,http://your_username:your_password@isp.decodo.com:10001' \
  -depth 3 \
  -c 2 \
  -exit-on-inactivity 5m
```

## Best Practices

### 1. Rotation Strategy
- Use multiple proxy endpoints for better distribution
- Rotate proxies to avoid rate limiting
- Monitor proxy performance and switch if needed

### 2. Geographic Targeting
- Choose proxy locations that match your target regions
- Use local proxies for better Google Maps results accuracy
- Consider time zones when scraping location-specific data

### 3. Performance Optimization
- Start with lower concurrency (-c 2) when testing
- Gradually increase concurrency based on proxy performance
- Monitor response times and adjust accordingly

### 4. Error Handling
- Implement retry logic for failed requests
- Monitor for IP blocks or rate limits
- Have backup proxies ready

## Troubleshooting

### Common Issues

**Connection Errors**
- Verify proxy credentials are correct
- Check if proxy endpoint is accessible
- Ensure proper URL formatting

**Authentication Failures**
- Double-check username and password
- Verify account is active and has credits
- Contact Decodo support if issues persist

**Performance Issues**
- Try different proxy locations
- Reduce concurrency level
- Check network connectivity

### Testing Your Decodo Proxy

Before running large scraping jobs, test your proxy connection:

```bash
# Test with a single query and minimal depth
./google-maps-scraper \
  -input test-query.txt \
  -results test-results.csv \
  -proxies 'http://your_username:your_password@premium.decodo.com:8080' \
  -depth 1 \
  -c 1 \
  -debug
```

Expected successful output:
```
✓ Proxy connection established: premium.decodo.com:8080
✓ Google Maps loaded successfully
✓ Processing query: test restaurant
✓ Found 10 results, extracting...
```

### Getting Help

- **24/7 Support**: Contact Decodo's technical support team
- **Documentation**: Access comprehensive guides at their platform
- **Community**: Join discussions and get help from other users
- **Test Connection**: Use the built-in `-debug` flag to verify proxy connectivity

## Why Choose Decodo for Google Maps Scraping?

1. **Reliability**: Industry-leading uptime and stability
2. **Speed**: Fastest response times for efficient scraping
3. **Scale**: Handle large-scale scraping operations
4. **Compliance**: Stay compliant with rate limits and restrictions
5. **Support**: Expert assistance when you need it


## Pricing and Plans

Visit [Decodo](https://visit.decodo.com/APVbbx) to explore their pricing options and find the plan that best suits your scraping needs.

- **Free Trial**: 3-day trial with 100MB of data
- **Flexible Plans**: Pay-as-you-go and subscription options
- **Enterprise Solutions**: Custom solutions for large-scale operations

---

**Ready to get started?** [Sign up for your free trial →](https://visit.decodo.com/APVbbx)
