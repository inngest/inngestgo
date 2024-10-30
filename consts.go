package inngestgo

const (
	SDKAuthor   = "inngest"
	SDKLanguage = "go"
	SDKVersion  = "0.7.4"

	SyncKindInBand    = "in_band"
	SyncKindOutOfBand = "out_of_band"
)

const (
	defaultAPIOrigin      = "https://api.inngest.com"
	defaultConnectOrigins = "wss://connect1.inngest.com,wss://connect2.inngest.com"
	defaultEventAPIOrigin = "https://inn.gs"
	devServerOrigin       = "http://127.0.0.1:8288"
)
