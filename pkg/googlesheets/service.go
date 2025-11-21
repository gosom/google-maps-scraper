package googlesheets

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	oauth2api "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

// Service handles interactions with Google APIs
type Service struct{}

// NewService creates a new Google Sheets service instance
func NewService() *Service {
	return &Service{}
}

// UploadCSV uploads a CSV file to Google Drive and converts it to a Google Sheet
// It returns the web view link of the created sheet
func (s *Service) UploadCSV(ctx context.Context, client *http.Client, filename string, data io.Reader) (string, error) {
	// Initialize Drive service
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve Drive client: %v", err)
	}

	// Define file metadata
	f := &drive.File{
		Name:     filename,
		MimeType: "application/vnd.google-apps.spreadsheet", // Convert to Google Sheet
	}

	// Upload and convert the file
	// We must specify the content type of the source data (CSV) so Drive knows how to convert it
	res, err := srv.Files.Create(f).Media(data, googleapi.ContentType("text/csv")).Do()
	if err != nil {
		return "", fmt.Errorf("unable to upload file: %v", err)
	}

	// Get the file's web view link
	// We need to fetch the file again to get the WebViewLink as Create might not return all fields by default
	// or we can request it in fields. Let's fetch it to be safe and simple.
	file, err := srv.Files.Get(res.Id).Fields("webViewLink").Do()
	if err != nil {
		return "", fmt.Errorf("unable to retrieve file details: %v", err)
	}

	return file.WebViewLink, nil
}

// GetUserInfo retrieves the email of the authenticated Google user
func (s *Service) GetUserInfo(ctx context.Context, client *http.Client) (string, error) {
	// Initialize OAuth2 service
	srv, err := oauth2api.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("unable to retrieve OAuth2 client: %v", err)
	}

	// Get user info
	userinfo, err := srv.Userinfo.Get().Do()
	if err != nil {
		return "", fmt.Errorf("unable to get user info: %v", err)
	}

	return userinfo.Email, nil
}
