package main

import (
	"errors"
	"os"
	"sync"
	"time"

	_json "encoding/json"

	"github.com/gorilla/websocket"
)

/* Constants */
const FAILED_HEARTBEAT_ACKS time.Duration = 5 * time.Millisecond

type OP int

const (
	DISPATCH              OP = 0
	HEARTBEAT             OP = 1
	IDENTIFY              OP = 2
	PRESENCE_UPDATE       OP = 3
	VOICE_STATE_UPDATE    OP = 4
	VOICE_PING            OP = 5
	RESUME                OP = 6
	RECONNECT             OP = 7
	REQUEST_GUILD_MEMBERS OP = 8
	INVALID_SESSION       OP = 9
	HELLO                 OP = 10
	HEARTBEAT_ACK         OP = 11
)

/* Errors */
var ErrInvalidToken = errors.New("an invalid main token was provided")
var ErrInvalidPaymentResponse = errors.New("got unexpected response while fetching payment sources")
var ErrWebsocketAlreadyConnected = errors.New("the websocket is already connected")

/* Session */
type Session struct {
	sync.RWMutex

	Token             string
	Dialer            *websocket.Dialer
	LastHeartbeatAck  time.Time
	LastHeartbeatSent time.Time
	Identify          IdentifyData
	State             string
	User              User
	CloseC            chan os.Signal
	Reconnecting      bool

	Guilds   map[string]*Guild
	Vanities map[string]string

	websocket         *websocket.Conn
	heartbeatInterval *time.Duration
	sequence          *int64
	gateway           string
	sessionID         string
	C                 chan os.Signal
}

/* HTTP Responses */
type RedeemResponse struct {
	Message          *string `json:"message"`
	Code             *int    `json:"code"`
	SubscriptionPlan *struct {
		Name string `json:"name"`
	} `json:"subscription_plan"`
}

type PaymentSource struct {
	ID string `json:"id"`
}

type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

/* Events */
type Event struct {
	Operation OP               `json:"op"`
	Sequence  int64            `json:"s"`
	Type      string           `json:"t"`
	RawData   _json.RawMessage `json:"d"`
}

type HeartbeatSendEvent struct {
	Op   int   `json:"op"`
	Data int64 `json:"d"`
}

type HelloEvent struct {
	HeartbeatInterval time.Duration `json:"heartbeat_interval"`
}

type DispatchReadyEvent struct {
	SessionID string  `json:"session_id"`
	Guilds    []Guild `json:"guilds"`
	User      User    `json:"user"`
}

type IdentifyEvent struct {
	Op   int          `json:"op"`
	Data IdentifyData `json:"d"`
}

type IdentifyData struct {
	Token      string `json:"token"`
	Properties struct {
		OS                     string  `json:"os"`
		Browser                string  `json:"browser"`
		Device                 string  `json:"device"`
		SystemLocale           string  `json:"system_locale"`
		BrowserUserAgent       string  `json:"browser_user_agent"`
		BrowserVersion         string  `json:"browser_version"`
		OSVersion              string  `json:"os_version"`
		Referrer               string  `json:"referrer"`
		ReferringDomain        string  `json:"referring_domain"`
		ReferrerCurrent        string  `json:"referrer_current"`
		ReferringDomainCurrent string  `json:"referring_domain_current"`
		ReleaseChannel         string  `json:"release_channel"`
		ClientBuildNumber      int     `json:"client_build_number"`
		ClientEventSource      *string `json:"client_event_source"`
	} `json:"properties"`
}

type ResumeEvent struct {
	Op   int `json:"op"`
	Data struct {
		Token     string `json:"token"`
		SessionID string `json:"session_id"`
		Sequence  int64  `json:"seq"`
	} `json:"d"`
}

/* Data Objects */
type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Banner      string `json:"banner"`
	Verified    bool   `json:"verified"`
	Avatar      string `json:"avatar"`
	Email       string `json:"email"`
	DisplayName string `json:"global_name"`
}

type Message struct {
	ID        string    `json:"id"`
	ChannelID string    `json:"channel_id"`
	GuildID   string    `json:"guild_id,omitempty"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Author    *User     `json:"author"`
}

type Guild struct {
	Name          string     `json:"name"`
	ID            string     `json:"id"`
	Channels      []*Channel `json:"channels"`
	VanityURLCode string     `json:"vanity_url_code"`
	Unavailable   bool       `json:"unavailable"`
	AFKChannelID  string     `json:"afk_channel_id"`
}
