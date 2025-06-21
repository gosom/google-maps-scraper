package lambdaaws

type lInput struct {
	JobID            string   `json:"job_id"`
	Part             int      `json:"part"`
	BucketName       string   `json:"bucket_name"`
	Keywords         []string `json:"keywords"`
	Depth            int      `json:"depth"`
	Concurrency      int      `json:"concurrency"`
	Language         string   `json:"language"`
	FunctionName     string   `json:"function_name"`
	DisablePageReuse bool     `json:"disable_page_reuse"`
	ExtraReviews     bool     `json:"extra_reviews"`
}
