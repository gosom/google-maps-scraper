# Google Sheets Plugin

This plugin sends scraped Google Maps data to Google Sheets via a webhook to Google Apps Script.

## Setup

1. Copy the environment template:
   ```bash
   cp .env.example .env
   ```

2. Edit `.env` with your values:
   ```bash
   URL_WEBHOOK=https://script.google.com/macros/s/YOUR_SCRIPT_ID/exec
   SHEET_NAME=scraping
   ```

3. Build the plugin:
   ```bash
   go build -buildmode=plugin -o googlesheets.so plugins/googlesheets/*.go
   ```

## Usage

Run with the plugin:
```bash
./google-maps-scraper -input queries.txt -plugin googlesheets.so
```

## Environment Variables

- `URL_WEBHOOK`: Google Apps Script webhook URL (required)
- `SHEET_NAME`: Target sheet name (defaults to 'scraping')

## Features

- Automatic field mapping to match Google Apps Script schema
- Duplicate detection by title
- Error handling and logging
- Continues processing even if individual entries fail
- JSON serialization for complex fields