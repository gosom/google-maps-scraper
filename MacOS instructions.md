**1. Install Homebrew**

Open the Terminal and run:

`/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`

After installation, add Homebrew to your PATH (if it doesn’t happen automatically). Run:

```
echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zshrc
source ~/.zshrc
```

Now, check if Homebrew is installed correctly:

`brew --version
`

**2. Install Go Using Homebrew**

Once Homebrew is installed, install Go with:

`brew install go`

After installing Go, verify it:

`go version`

**3. Clone the Repository**

Open your terminal and clone the repository:

```
git clone https://github.com/gosom/google-maps-scraper.git
cd google-maps-scraper
```

**4. Build for macOS**

The repository provides a Makefile, but you can manually compile the project for macOS:

`go build -o google_maps_scraper`

Create a macOS application structure:

```
mkdir -p GoogleMapsScraper.app/Contents/MacOS
mv google_maps_scraper GoogleMapsScraper.app/Contents/MacOS/
```

Convert the App to a GUI Application

Create a new script inside MacOS/:

`nano GoogleMapsScraper.app/Contents/MacOS/start.sh`

Paste this inside:

```
#!/bin/bash
DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$HOME"

# Run scraper in background and get its PID
"$DIR/google_maps_scraper" > "$HOME/google_maps_scraper.log" 2>&1 &
SCRAPER_PID=$!

# Open browser
sleep 2
open http://localhost:8080

# Idle timeout in seconds
IDLE_TIMEOUT=6000

# Wait and kill
sleep $IDLE_TIMEOUT

# Check if still running, and kill it
if ps -p $SCRAPER_PID > /dev/null; then
  echo "Stopping scraper after $IDLE_TIMEOUT seconds of idle time."
  kill $SCRAPER_PID
fi
```
Save and exit 

Now make it executable:

`chmod +x GoogleMapsScraper.app/Contents/MacOS/start.sh`

Add a Info.plist inside GoogleMapsScraper.app/Contents/:

```
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
    <dict>
        <key>CFBundleExecutable</key>
        <string>start.sh</string>
        <key>CFBundleIdentifier</key>
        <string>com.yourcompany.googlemapsscraper</string>
        <key>CFBundleName</key>
        <string>Google Maps Scraper</string>
        <key>CFBundleVersion</key>
        <string>1.0</string>
    </dict>
</plist>
```

**5. Run the Application**

`open GoogleMapsScraper.app`

**6. Code Sign the App (Optional but Recommended)**

`codesign --force --deep --sign - GoogleMapsScraper.app`

If you plan to distribute it, you’ll need an Apple Developer ID to properly sign it.

**6. Create a .dmg Installer (Optional)**

```
mkdir -p ~/dmg-tmp/GoogleMapsScraper
cp -R /path/to/GoogleMapsScraper.app ~/dmg-tmp/GoogleMapsScraper/
```
Replace /path/to/GoogleMapsScraper.app with your actual path (e.g. ~/Desktop/GoogleMapsScraper.app)

Create the .dmg

```
hdiutil create -volname "GoogleMapsScraper" -srcfolder ~/dmg-tmp/GoogleMapsScraper -ov -format UDZO GoogleMapsScraper.dmg
```
You’ll now have a file named GoogleMapsScraper.dmg in the current folder.

An example of the built .app can be seen here: https://github.com/melogabriel/google-maps-scraper/releases/tag/v1.0.0
