package testutils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"

	proto "github.com/VectorEngineering/vector-protobuf-definitions/api-definitions/pkg/generated/lead_scraper_service/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Package testutils provides utility functions for generating test data,
// particularly focused on creating realistic user and business account data
// for testing purposes.

const (
	EMPTY         = ""
	letterBytes   = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	numberBytes   = "0123456789"
	specialChars  = "!@#$%^&*()_+-=[]{}|;:,.<>?"
	letterIdxBits = 6
	letterIdxMask = 1<<letterIdxBits - 1
	letterIdxMax  = 63 / letterIdxBits
)

var (
	// Thread-safe random source
	rndMu sync.Mutex
	src   = rand.Reader

	// Common domains for email generation
	emailDomains = []string{
		"gmail.com", "yahoo.com", "hotmail.com", "outlook.com",
		"example.com", "test.com", "company.com",
	}

	// Common company industries
	industries = []string{
		"Technology", "Healthcare", "Finance", "Education",
		"Manufacturing", "Retail", "Construction", "Entertainment",
	}

	// Common US states
	states = []string{
		"CA", "NY", "TX", "FL", "IL", "PA", "OH", "GA", "NC", "MI",
	}

	// Common cities
	cities = []string{
		"New York", "Los Angeles", "Chicago", "Houston", "Phoenix",
		"Philadelphia", "San Antonio", "San Diego", "Dallas", "San Jose",
	}

	// Common payment methods
	paymentMethods = []string{
		"VISA", "MasterCard", "AMEX", "PayPal", "Cash", "Apple Pay", "Google Pay",
	}

	// Common social media platforms
	socialMediaPlatforms = []string{
		"facebook", "twitter", "linkedin", "instagram", "youtube",
	}

	// Common business types
	businessTypes = []string{
		"Corporation", "LLC", "Partnership", "Sole Proprietorship", "Non-Profit",
	}

	// Common funding stages
	fundingStages = []string{
		"Seed", "Series A", "Series B", "Series C", "IPO", "Bootstrapped",
	}

	// Common CMS platforms
	cmsPlatforms = []string{
		"WordPress", "Shopify", "Wix", "Squarespace", "Drupal", "Magento",
	}

	// Common ecommerce platforms
	ecommercePlatforms = []string{
		"Shopify", "WooCommerce", "Magento", "BigCommerce", "PrestaShop",
	}

	// Common energy sources
	energySources = []string{
		"Solar", "Wind", "Hydro", "Nuclear", "Natural Gas", "Coal",
	}

	// Common job statuses
	jobStatuses = []string{
		"QUEUED", "RUNNING", "COMPLETED", "FAILED", "CANCELLED",
	}
)

// GenerateRandomString creates a random string of specified length with optional
// inclusion of numbers and special characters.
//
// Parameters:
//   - n: The length of the string to generate
//   - includeNumbers: Whether to include numerical digits in the output
//   - includeSpecial: Whether to include special characters in the output
//
// Returns:
//   - A random string of the specified length and characteristics
func GenerateRandomString(n int, includeNumbers bool, includeSpecial bool) string {
	if n <= 0 {
		return EMPTY
	}

	var chars string
	chars = letterBytes
	if includeNumbers {
		chars += numberBytes
	}
	if includeSpecial {
		chars += specialChars
	}

	result := make([]byte, n)
	for i := 0; i < n; i++ {
		num, err := rand.Int(src, big.NewInt(int64(len(chars))))
		if err != nil {
			panic(fmt.Sprintf("failed to generate random string: %v", err))
		}
		result[i] = chars[num.Int64()]
	}

	return string(result)
}

// GenerateRandomInt generates a cryptographically secure random integer
func GenerateRandomInt(min, max int) int {
	if min >= max {
		return min
	}

	n, err := rand.Int(src, big.NewInt(int64(max-min+1)))
	if err != nil {
		panic(fmt.Sprintf("failed to generate random int: %v", err))
	}
	return int(n.Int64()) + min
}

// GenerateRandomFloat generates a random float64 within specified range
func GenerateRandomFloat(min, max float64) float64 {
	n, err := rand.Int(src, big.NewInt(1<<53))
	if err != nil {
		panic(fmt.Sprintf("failed to generate random float: %v", err))
	}
	return min + (float64(n.Int64())/float64(1<<53))*(max-min)
}

// GenerateRandomEmail generates a realistic-looking email address
func GenerateRandomEmail(namelen int) string {
	username := GenerateRandomString(namelen, true, false)
	domain := emailDomains[GenerateRandomInt(0, len(emailDomains)-1)]
	return fmt.Sprintf("%s@%s", strings.ToLower(username), domain)
}

// GenerateRandomIP generates a random IP address
func GenerateRandomIP() string {
	ip := make(net.IP, 4)
	_, err := rand.Read(ip)
	if err != nil {
		panic(fmt.Sprintf("failed to generate random IP: %v", err))
	}
	return ip.String()
}

// GenerateRandomTimestamp generates a random timestamp within a range
func GenerateRandomTimestamp(start, end time.Time) *timestamppb.Timestamp {
	min := start.Unix()
	max := end.Unix()
	delta := max - min

	sec, err := rand.Int(src, big.NewInt(delta))
	if err != nil {
		panic(fmt.Sprintf("failed to generate random timestamp: %v", err))
	}

	randomTime := time.Unix(min+sec.Int64(), 0)
	return timestamppb.New(randomTime)
}

func GenerateRandomizeUrl() string {
	return fmt.Sprintf("https://%s.com", GenerateRandomString(10, false, false))
}

// GenerateRandomizedAccount creates a new UserAccount instance populated with
// realistic random data suitable for testing purposes. The generated account
// includes random but plausible values for all fields including personal
// information, settings, and metadata.
//
// Returns:
//   - A pointer to a new UserAccount instance with randomized data
func GenerateRandomizedAccount() *proto.Account {	
	return &proto.Account{
		Email:               GenerateRandomEmail(10),
		AuthPlatformUserId:  fmt.Sprintf("auth0|%s", GenerateRandomString(24, true, false)),
		OrgId:               fmt.Sprintf("org_%s", GenerateRandomString(10, true, false)),
		TenantId:            fmt.Sprintf("tenant_%s", GenerateRandomString(10, true, false)),
		AccountStatus:       proto.Account_AccountStatus(GenerateRandomInt(0, 2)),
		Roles:               []string{"role_1", "role_2"},
		Permissions:         []string{"permission_1", "permission_2"},
		MfaEnabled:          false,
		LastLoginAt:         nil,
		TotalJobsRun:        0,
		MonthlyJobLimit:     int32(GenerateRandomInt(1, 100)),
		ConcurrentJobLimit:  int32(GenerateRandomInt(1, 100)),
		Workspaces:          nil,
		Settings:            nil,
		Timezone:            "America/New_York",
	}
}

func GenerateRandomWorkspace() *proto.Workspace {
	return &proto.Workspace{
		Name: GenerateRandomString(10, false, false),
		Industry: industries[GenerateRandomInt(0, len(industries)-1)],
		Domain: GenerateRandomizeUrl(),
		GdprCompliant: false,
		HipaaCompliant: false,
		Soc2Compliant: false,
		StorageQuota: int64(GenerateRandomInt(1, 1000000)),
		UsedStorage: int64(GenerateRandomInt(1, 1000000)),
		CreatedAt: nil,
		UpdatedAt: nil,
		DeletedAt: nil,
		Workflows: nil,
		JobsRunThisMonth: 0,
		WorkspaceJobLimit: 0,
		DailyJobQuota:     0,
		ActiveScrapers:     0,
		TotalLeadsCollected: 0,
		LastJobRun:          nil,
	}
}

func GenerateRandomizedScrapingJob() *proto.ScrapingJob {
	now := time.Now()
	startDate := now.AddDate(-5, 0, 0) // 5 years ago

	return &proto.ScrapingJob{
		CreatedAt: GenerateRandomTimestamp(startDate, now),
		Status: proto.BackgroundJobStatus(GenerateRandomInt(0, 2)),
		Priority: int32(GenerateRandomInt(0, 100)),
		PayloadType: "payload_type",
		Payload: []byte(GenerateRandomString(100, true, false)),
		Name: GenerateRandomString(10, false, false),
		Keywords: []string{"keyword_1", "keyword_2"},
		Lang: "en",
		Zoom: int32(GenerateRandomInt(1, 20)),
		Lat: "40.7128",
		Lon: "-74.0060",
		FastMode: false,
		Radius: int32(GenerateRandomInt(1, 1000000)),
		MaxTime: int32(GenerateRandomInt(1, 3600)),
	}
}

// GenerateRandomReview creates a new Review instance with random test data
func GenerateRandomReview() *proto.Review {
	now := time.Now()
	startDate := now.AddDate(-1, 0, 0) // Up to 1 year ago

	return &proto.Review{
		Author:          GenerateRandomString(10, false, false),
		Rating:          float32(GenerateRandomFloat(1.0, 5.0)),
		Text:            GenerateRandomString(100, true, true),
		Time:            GenerateRandomTimestamp(startDate, now),
		Language:        "en",
		ProfilePhotoUrl: GenerateRandomizeUrl(),
		ReviewCount:     int32(GenerateRandomInt(1, 1000)),
		CreatedAt:       nil,
		UpdatedAt:       nil,
		DeletedAt:       nil,
	}
}

// GenerateRandomBusinessHours creates a new BusinessHours instance with random test data
func GenerateRandomBusinessHours() *proto.BusinessHours {
	return &proto.BusinessHours{
		Day:       proto.BusinessHours_DayOfWeek(GenerateRandomInt(0, 6)),
		OpenTime:  fmt.Sprintf("%02d:00", GenerateRandomInt(6, 12)),
		CloseTime: fmt.Sprintf("%02d:00", GenerateRandomInt(17, 23)),
		Closed:    GenerateRandomInt(0, 10) < 1, // 10% chance of being closed
		CreatedAt: nil,
		UpdatedAt: nil,
		DeletedAt: nil,
	}
}

// GenerateRandomLead creates a new Lead instance with random test data
func GenerateRandomLead() *proto.Lead {
	socialMedia := make(map[string]string)
	for _, platform := range socialMediaPlatforms {
		if GenerateRandomInt(0, 1) == 1 {
			socialMedia[platform] = GenerateRandomizeUrl()
		}
	}

	return &proto.Lead{
		Name:              fmt.Sprintf("%s Business", GenerateRandomString(10, false, false)),
		Website:           GenerateRandomizeUrl(),
		Phone:             fmt.Sprintf("+1%d", GenerateRandomInt(1000000000, 9999999999)),
		Address:           fmt.Sprintf("%d %s St", GenerateRandomInt(1, 999), GenerateRandomString(8, false, false)),
		City:              cities[GenerateRandomInt(0, len(cities)-1)],
		State:             states[GenerateRandomInt(0, len(states)-1)],
		Country:           "USA",
		Latitude:          GenerateRandomFloat(-90, 90),
		Longitude:         GenerateRandomFloat(-180, 180),
		GoogleRating:      float32(GenerateRandomFloat(1.0, 5.0)),
		ReviewCount:       int32(GenerateRandomInt(0, 1000)),
		Industry:          industries[GenerateRandomInt(0, len(industries)-1)],
		EmployeeCount:     int32(GenerateRandomInt(1, 1000)),
		EstimatedRevenue:  int64(GenerateRandomInt(100000, 10000000)),
		OrgId:             fmt.Sprintf("org_%s", GenerateRandomString(10, true, false)),
		TenantId:          fmt.Sprintf("tenant_%s", GenerateRandomString(10, true, false)),
		CreatedAt:         nil,
		UpdatedAt:         nil,
		DeletedAt:         nil,
		PlaceId:           fmt.Sprintf("ChIJ_%s", GenerateRandomString(20, true, false)),
		GoogleMapsUrl:     fmt.Sprintf("https://maps.google.com/?q=%f,%f", GenerateRandomFloat(-90, 90), GenerateRandomFloat(-180, 180)),
		BusinessStatus:    "OPERATIONAL",
		MainPhotoUrl:      GenerateRandomizeUrl(),
		SocialMedia:       socialMedia,
		BusinessType:      businessTypes[GenerateRandomInt(0, len(businessTypes)-1)],
		FundingStage:      fundingStages[GenerateRandomInt(0, len(fundingStages)-1)],
		IsPublicCompany:   GenerateRandomInt(0, 1) == 1,
		WebsiteLoadSpeed:  float32(GenerateRandomFloat(0.1, 5.0)),
		HasSslCertificate: GenerateRandomInt(0, 1) == 1,
		CmsUsed:           cmsPlatforms[GenerateRandomInt(0, len(cmsPlatforms)-1)],
		Timezone:          "America/New_York",
	}
}

// GenerateRandomAccountSettings creates a new AccountSettings instance with random test data
func GenerateRandomAccountSettings() *proto.AccountSettings {
	return &proto.AccountSettings{
		EmailNotifications: GenerateRandomInt(0, 1) == 1,
		SlackNotifications: GenerateRandomInt(0, 1) == 1,
		AutoPurgeEnabled:   GenerateRandomInt(0, 1) == 1,
		Require_2Fa:        GenerateRandomInt(0, 1) == 1,
		CreatedAt:          nil,
		UpdatedAt:          nil,
		DeletedAt:          nil,
	}
}

// GenerateRandomAPIKey creates a new APIKey instance with random test data
func GenerateRandomAPIKey() *proto.APIKey {
	keyPrefix := GenerateRandomString(8, true, false)
	return &proto.APIKey{
		Name:                fmt.Sprintf("API Key %s", GenerateRandomString(6, false, false)),
		KeyHash:             fmt.Sprintf("hash_%s", GenerateRandomString(32, true, false)),
		KeyPrefix:           keyPrefix,
		OrgId:               fmt.Sprintf("org_%s", GenerateRandomString(10, true, false)),
		TenantId:            fmt.Sprintf("tenant_%s", GenerateRandomString(10, true, false)),
		Scopes:              []string{"read", "write"},
		AllowedIps:          []string{GenerateRandomIP(), GenerateRandomIP()},
		IsTestKey:           GenerateRandomInt(0, 1) == 1,
		RequestsPerSecond:   int32(GenerateRandomInt(1, 100)),
		RequestsPerDay:      int32(GenerateRandomInt(1000, 10000)),
		ConcurrentRequests:  int32(GenerateRandomInt(1, 10)),
		MonthlyRequestQuota: int64(GenerateRandomInt(10000, 1000000)),
		Status:              proto.APIKey_Status(GenerateRandomInt(0, 2)),
		CreatedAt:           nil,
		UpdatedAt:           nil,
		DeletedAt:           nil,
	}
}

// GenerateRandomScrapingWorkflow creates a new ScrapingWorkflow instance with random test data
func GenerateRandomScrapingWorkflow() *proto.ScrapingWorkflow {
	return &proto.ScrapingWorkflow{
		CronExpression:         "0 0 * * *", // Daily at midnight
		Status:                 proto.WorkflowStatus(GenerateRandomInt(0, 2)),
		RetryCount:             int32(GenerateRandomInt(0, 5)),
		MaxRetries:             5,
		AlertEmails:            GenerateRandomEmail(8),
		OrgId:                  fmt.Sprintf("org_%s", GenerateRandomString(10, true, false)),
		TenantId:               fmt.Sprintf("tenant_%s", GenerateRandomString(10, true, false)),
		CreatedAt:              nil,
		UpdatedAt:              nil,
		DeletedAt:              nil,
		GeoFencingRadius:       float32(GenerateRandomFloat(1000, 10000)),
		GeoFencingLat:          GenerateRandomFloat(-90, 90),
		GeoFencingLon:          GenerateRandomFloat(-180, 180),
		GeoFencingZoomMin:      int32(GenerateRandomInt(1, 10)),
		GeoFencingZoomMax:      int32(GenerateRandomInt(11, 20)),
		IncludeReviews:         GenerateRandomInt(0, 1) == 1,
		IncludePhotos:          GenerateRandomInt(0, 1) == 1,
		IncludeBusinessHours:   GenerateRandomInt(0, 1) == 1,
		MaxReviewsPerBusiness:  int32(GenerateRandomInt(10, 100)),
		OutputFormat:           proto.ScrapingWorkflow_OutputFormat(GenerateRandomInt(0, 2)),
		OutputDestination:      fmt.Sprintf("s3://bucket-%s", GenerateRandomString(8, true, false)),
		AnonymizePii:           GenerateRandomInt(0, 1) == 1,
		RespectRobotsTxt:      true,
		AcceptTermsOfService:   true,
		UserAgent:              fmt.Sprintf("TestBot/%s", GenerateRandomString(8, true, false)),
	}
}

// GenerateRandomResult creates a new Result instance with random test data
func GenerateRandomResult() *proto.Result {
	return &proto.Result{
		Id:   int32(GenerateRandomInt(1, 1000000)),
		Data: []byte(fmt.Sprintf(`{"data": "%s"}`, GenerateRandomString(100, true, true))),
	}
}

// GenerateRandomAccounts generates a slice of random Account instances
func GenerateRandomAccounts(count int) []*proto.Account {
	accounts := make([]*proto.Account, count)
	for i := 0; i < count; i++ {
		accounts[i] = GenerateRandomizedAccount()
	}
	return accounts
}

// GenerateRandomWorkspaces generates a slice of random Workspace instances
func GenerateRandomWorkspaces(count int) []*proto.Workspace {
	workspaces := make([]*proto.Workspace, count)
	for i := 0; i < count; i++ {
		workspaces[i] = GenerateRandomWorkspace()
	}
	return workspaces
}

// GenerateRandomScrapingJobs generates a slice of random ScrapingJob instances
func GenerateRandomScrapingJobs(count int) []*proto.ScrapingJob {
	jobs := make([]*proto.ScrapingJob, count)
	for i := 0; i < count; i++ {
		jobs[i] = GenerateRandomizedScrapingJob()
	}
	return jobs
}

// GenerateRandomReviews generates a slice of random Review instances
func GenerateRandomReviews(count int) []*proto.Review {
	reviews := make([]*proto.Review, count)
	for i := 0; i < count; i++ {
		reviews[i] = GenerateRandomReview()
	}
	return reviews
}

// GenerateRandomBusinessHoursList generates a slice of random BusinessHours instances
func GenerateRandomBusinessHoursList(count int) []*proto.BusinessHours {
	hours := make([]*proto.BusinessHours, count)
	for i := 0; i < count; i++ {
		hours[i] = GenerateRandomBusinessHours()
	}
	return hours
}

// GenerateRandomLeads generates a slice of random Lead instances
func GenerateRandomLeads(count int) []*proto.Lead {
	leads := make([]*proto.Lead, count)
	for i := 0; i < count; i++ {
		leads[i] = GenerateRandomLead()
	}
	return leads
}

// GenerateRandomAccountSettingsList generates a slice of random AccountSettings instances
func GenerateRandomAccountSettingsList(count int) []*proto.AccountSettings {
	settings := make([]*proto.AccountSettings, count)
	for i := 0; i < count; i++ {
		settings[i] = GenerateRandomAccountSettings()
	}
	return settings
}

// GenerateRandomAPIKeys generates a slice of random APIKey instances
func GenerateRandomAPIKeys(count int) []*proto.APIKey {
	keys := make([]*proto.APIKey, count)
	for i := 0; i < count; i++ {
		keys[i] = GenerateRandomAPIKey()
	}
	return keys
}

// GenerateRandomScrapingWorkflows generates a slice of random ScrapingWorkflow instances
func GenerateRandomScrapingWorkflows(count int) []*proto.ScrapingWorkflow {
	workflows := make([]*proto.ScrapingWorkflow, count)
	for i := 0; i < count; i++ {
		workflows[i] = GenerateRandomScrapingWorkflow()
	}
	return workflows
}

// GenerateRandomResults generates a slice of random Result instances
func GenerateRandomResults(count int) []*proto.Result {
	results := make([]*proto.Result, count)
	for i := 0; i < count; i++ {
		results[i] = GenerateRandomResult()
	}
	return results
}

// GenerateRandomWithOptions is a generic function that generates a slice of random instances
// with optional configuration
func GenerateRandomWithOptions[T any](count int, generator func() T) []T {
	items := make([]T, count)
	for i := 0; i < count; i++ {
		items[i] = generator()
	}
	return items
}

// GenerateConfig holds configuration for generating test data
type GenerateConfig struct {
	// Number of related records to generate
	NumWorkflows        int
	NumScrapingJobs     int
	NumLeads            int
	NumReviews          int
	NumBusinessHours    int
	NumAPIKeys          int
	NumAccountSettings  int
	NumResults          int
	// Whether to generate related records
	WithWorkflows       bool
	WithScrapingJobs    bool
	WithLeads           bool
	WithReviews         bool
	WithBusinessHours   bool
	WithAPIKeys         bool
	WithAccountSettings bool
	WithResults         bool
}

// DefaultGenerateConfig returns a default configuration
func DefaultGenerateConfig() *GenerateConfig {
	return &GenerateConfig{
		NumWorkflows:        2,
		NumScrapingJobs:     3,
		NumLeads:            5,
		NumReviews:          10,
		NumBusinessHours:    7,
		NumAPIKeys:          2,
		NumAccountSettings:  1,
		NumResults:          5,
		WithWorkflows:       true,
		WithScrapingJobs:    true,
		WithLeads:           true,
		WithReviews:         true,
		WithBusinessHours:   true,
		WithAPIKeys:         true,
		WithAccountSettings: true,
		WithResults:         true,
	}
}

// TestContext represents a complete test context with all related records
type TestContext struct {
	Account          *proto.Account
	Workspace        *proto.Workspace
	Workflows        []*proto.ScrapingWorkflow
	ScrapingJobs     []*proto.ScrapingJob
	Leads            []*proto.Lead
	Reviews          []*proto.Review
	BusinessHours    []*proto.BusinessHours
	APIKeys          []*proto.APIKey
	AccountSettings  *proto.AccountSettings
	Results          []*proto.Result
}

// GenerateTestContext generates a complete test context with related records based on config
func GenerateTestContext(config *GenerateConfig) *TestContext {
	if config == nil {
		config = DefaultGenerateConfig()
	}

	// Generate primary records
	account := GenerateRandomizedAccount()
	workspace := GenerateRandomWorkspace()

	// Link workspace to account
	if account.Workspaces == nil {
		account.Workspaces = []*proto.Workspace{workspace}
	} else {
		account.Workspaces = append(account.Workspaces, workspace)
	}

	ctx := &TestContext{
		Account:   account,
		Workspace: workspace,
	}

	// Generate related records based on config
	if config.WithWorkflows {
		ctx.Workflows = GenerateRandomWorkflowsForWorkspace(workspace, config.NumWorkflows)
	}

	if config.WithScrapingJobs {
		ctx.ScrapingJobs = GenerateRandomScrapingJobsForWorkspace(workspace, config.NumScrapingJobs)
	}

	if config.WithLeads {
		ctx.Leads = GenerateRandomLeadsForWorkspace(workspace, config.NumLeads)
	}

	if config.WithReviews {
		ctx.Reviews = GenerateRandomReviews(config.NumReviews)
	}

	if config.WithBusinessHours {
		ctx.BusinessHours = GenerateRandomBusinessHoursList(config.NumBusinessHours)
	}

	if config.WithAPIKeys {
		ctx.APIKeys = GenerateRandomAPIKeysForWorkspace(workspace, config.NumAPIKeys)
	}

	if config.WithAccountSettings {
		ctx.AccountSettings = GenerateRandomAccountSettings()
	}

	if config.WithResults {
		ctx.Results = GenerateRandomResults(config.NumResults)
	}

	return ctx
}

// GenerateRandomWorkflowsForWorkspace generates workflows linked to a workspace
func GenerateRandomWorkflowsForWorkspace(workspace *proto.Workspace, count int) []*proto.ScrapingWorkflow {
	workflows := make([]*proto.ScrapingWorkflow, count)
	for i := 0; i < count; i++ {
		workflow := GenerateRandomScrapingWorkflow()
		workflow.Workspace = workspace
		workflows[i] = workflow
	}
	return workflows
}

// GenerateRandomScrapingJobsForWorkspace generates scraping jobs linked to a workspace
func GenerateRandomScrapingJobsForWorkspace(workspace *proto.Workspace, count int) []*proto.ScrapingJob {
	jobs := make([]*proto.ScrapingJob, count)
	for i := 0; i < count; i++ {
		job := GenerateRandomizedScrapingJob()
		jobs[i] = job
	}
	return jobs
}

// GenerateRandomLeadsForWorkspace generates leads linked to a workspace
func GenerateRandomLeadsForWorkspace(workspace *proto.Workspace, count int) []*proto.Lead {
	leads := make([]*proto.Lead, count)
	for i := 0; i < count; i++ {
		lead := GenerateRandomLead()
		lead.Workspace = workspace
		leads[i] = lead
	}
	return leads
}

// GenerateRandomAPIKeysForWorkspace generates API keys linked to a workspace
func GenerateRandomAPIKeysForWorkspace(workspace *proto.Workspace, count int) []*proto.APIKey {
	keys := make([]*proto.APIKey, count)
	for i := 0; i < count; i++ {
		key := GenerateRandomAPIKey()
		key.Workspace = workspace
		keys[i] = key
	}
	return keys
}
