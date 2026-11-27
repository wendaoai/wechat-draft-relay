package v1

import "time"

type CropPercent struct {
	Ratio string `json:"ratio"`
	X1    string `json:"x1"`
	Y1    string `json:"y1"`
	X2    string `json:"x2"`
	Y2    string `json:"y2"`
}

type CoverInfo struct {
	CropPercentList []CropPercent `json:"cropPercentList,omitempty"`
}

type Article struct {
	ArticleType        string     `json:"articleType,omitempty"`
	ImageIDs           []string   `json:"imageIds,omitempty"`
	Title              string     `json:"title"`
	Author             string     `json:"author"`
	Digest             string     `json:"digest"`
	ContentText        string     `json:"contentText,omitempty"`
	ContentHTML        string     `json:"contentHtml"`
	ContentSourceURL   string     `json:"contentSourceUrl"`
	CoverInfo          *CoverInfo `json:"coverInfo,omitempty"`
	NeedOpenComment    int        `json:"needOpenComment"`
	OnlyFansCanComment int        `json:"onlyFansCanComment"`
}

type Image struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	FileField string `json:"fileField"`
	SHA256    string `json:"sha256"`
}

type Payload struct {
	SchemaVersion   int     `json:"schemaVersion"`
	AppID           string  `json:"appid"`
	AppSecret       string  `json:"appsecret,omitempty"`
	UUID            string  `json:"uuid"`
	Article         Article `json:"article"`
	CoverImageID    string  `json:"coverImageId"`
	Images          []Image `json:"images"`
	ExpectedAttempt int     `json:"expectedAttempt,omitempty"`
	RetryRequestID  string  `json:"retryRequestId,omitempty"`
}

type Progress struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
	Percent   int `json:"percent"`
}

type LastRetry struct {
	RequestID       string `json:"requestId"`
	ExpectedAttempt int    `json:"expectedAttempt"`
	AcceptedAttempt int    `json:"acceptedAttempt"`
}

type Result struct {
	MediaID     string    `json:"mediaId"`
	CompletedAt time.Time `json:"completedAt"`
}

type Fault struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	WechatErrcode int    `json:"wechatErrcode,omitempty"`
	Details       any    `json:"details,omitempty"`
}

type Task struct {
	AppID            string     `json:"appid"`
	UUID             string     `json:"uuid"`
	Status           string     `json:"status"`
	Phase            string     `json:"phase"`
	Progress         Progress   `json:"progress"`
	Message          string     `json:"message"`
	Attempt          int        `json:"attempt"`
	LastRetry        *LastRetry `json:"lastRetry"`
	RetryAllowed     bool       `json:"retryAllowed"`
	CancelAllowed    bool       `json:"cancelAllowed"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	FinishedAt       *time.Time `json:"finishedAt"`
	ExpiresAt        *time.Time `json:"expiresAt"`
	Result           *Result    `json:"result"`
	Error            *Fault     `json:"error"`
	PollAfterSeconds int        `json:"pollAfterSeconds,omitempty"`
}

type Envelope struct {
	Data      any    `json:"data,omitempty"`
	Error     *Fault `json:"error,omitempty"`
	RequestID string `json:"requestId"`
}
