package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

func createClient(token string) (s *Session) {
	session := &Session{
		Token:    token,
		Dialer:   websocket.DefaultDialer,
		gateway:  "wss://gateway.discord.gg/?v=10&encoding=json",
		sequence: new(int64),
		Guilds:   map[string]*Guild{},
		Vanities: map[string]string{},
		C:        make(chan os.Signal),
	}

	signal.Notify(session.C, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	build, err := strconv.Atoi(*clientBuildNumber)

	if err != nil {
		logger.Errorf("Failed to convert build number (%s) to string: %v.", *clientBuildNumber, err)
		exit()
	}

	session.Identify.Token = token

	session.Identify.Properties.OS = config.Properties.OS
	session.Identify.Properties.Browser = config.Properties.Browser
	session.Identify.Properties.Device = config.Properties.Device
	session.Identify.Properties.SystemLocale = config.Properties.SystemLocale
	session.Identify.Properties.BrowserUserAgent = config.Properties.UserAgent
	session.Identify.Properties.BrowserVersion = config.Properties.BrowserVersion
	session.Identify.Properties.OSVersion = config.Properties.OSVersion
	session.Identify.Properties.Referrer = config.Properties.Referrer
	session.Identify.Properties.ReferringDomain = config.Properties.ReferringDomain
	session.Identify.Properties.ReferrerCurrent = config.Properties.ReferrerCurrent
	session.Identify.Properties.ReferringDomainCurrent = config.Properties.ReferringDomainCurrent
	session.Identify.Properties.ReleaseChannel = config.Properties.ReleaseChannel
	session.Identify.Properties.ClientBuildNumber = build
	session.Identify.Properties.ClientEventSource = nil

	return session
}

func (session *Session) Connect() error {
	var err error

	if session.websocket != nil {
		return ErrWebsocketAlreadyConnected
	}

	logger.Infof("Connecting with %v...", strip(session.Token, 35))

	header := http.Header{}
	header.Add("Accept-Encoding", "json")

	logger.Debugf("Connecting to %s", session.gateway)
	session.websocket, _, err = session.Dialer.Dial(session.gateway, header)

	if err != nil {
		logger.Errorf("Failed to connect to websocket: %v", err)
		session.websocket = nil

		return err
	}

	defer session.Close()

	session.websocket.SetCloseHandler(func(code int, text string) error {
		session.State = "CLOSED"
		session.C <- os.Interrupt

		return nil
	})

	go func() {
		for {
			if session.State == "CLOSED" || session.State == "CLOSING" {
				break
			}

			messageType, payload, err := session.websocket.ReadMessage()

			if err != nil {
				logger.Errorf("Received error while reading websocket message: %v", err)
				continue
			}

			err = session.onEvent(messageType, payload)

			if err != nil {
				logger.Errorf("Received error while decoding event: %v", err)
				continue
			}
		}
	}()

	<-session.C

	return nil
}

func (session *Session) Close() error {
	return session.CloseWithCode(websocket.CloseNormalClosure)
}

func (session *Session) CloseWithCode(code int) error {
	session.State = "CLOSING"

	err := session.websocket.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""))

	if err == nil {
		session.State = "CLOSED"
		session.C <- os.Interrupt

		delete(sessions, session.Token)

		if len(sessions) == 0 {
			interrupt <- os.Interrupt
		}
	} else {
		session.State = "CONNECTED"
	}

	return err
}

func (session *Session) onEvent(messageType int, message []byte) error {
	var err error

	var data *Event
	err = json.Unmarshal(message, &data)

	if err != nil {
		logger.Errorf("Error decoding websocket message: %v", err)
		return err
	}

	atomic.StoreInt64(session.sequence, data.Sequence)

	if data.Operation == DISPATCH {
		if data.Type == "READY" {
			session.State = "CONNECTED"
			event := DispatchReadyEvent{}

			if err = json.Unmarshal(data.RawData, &event); err != nil {
				logger.Errorf("Failed to unmarshal HELLO payload: %v", err)
				return err
			}

			for _, guild := range event.Guilds {
				guilds[guild.ID] = &guild

				if guild.VanityURLCode != "" {
					logger.Infof("Queued %v for sniping. (Vanity: %v)", guild.Name, guild.VanityURLCode)
					session.Vanities[guild.ID] = guild.VanityURLCode
				}
			}

			logger.Infof("Logged in as %v watching %v vanities.", event.User.Username, len(session.Vanities))
		}

		if data.Type == "GUILD_CREATE" {
			event := Guild{}

			if err = json.Unmarshal(data.RawData, &event); err != nil {
				logger.Errorf("Failed to unmarshal GUILD_CREATE payload: %v", err)
				return err
			}

			if event.VanityURLCode != "" {
				logger.Infof("Queued %v for sniping. (Vanity: %v)", event.Name, event.VanityURLCode)
				session.Vanities[event.ID] = event.VanityURLCode
			}
		}

		if data.Type == "GUILD_UPDATE" {
			event := Guild{}

			if err = json.Unmarshal(data.RawData, &event); err != nil {
				logger.Errorf("Failed to unmarshal GUILD_UPDATE payload: %v", err)
				return err
			}

			if event.Unavailable {
				return nil
			}

			if config.IgnoreHostGuilds && slices.Contains(config.Guilds, event.ID) {
				return nil
			}

			if event.VanityURLCode != session.Vanities[event.ID] {
				_, exists := session.Vanities[event.ID]

				if session.Vanities[event.ID] == "" {
					return nil
				}

				logger.Infof("Vanity URL changed: %v -> %v", If(exists, session.Vanities[event.ID], "None"), If(event.VanityURLCode != "", event.VanityURLCode, "None"))

				if config.SameGuildTimeout != 0 {
					interval, existing := sameGuildIntervals[config.Guilds[guildsIndex]]

					if existing {
						difference := time.Until(*interval)

						if difference > 0 {
							logger.Warnf("Guild %v is on timeout for %.2fs. Ignoring vanity change.", config.Guilds[guildsIndex], difference.Seconds())
							return nil
						} else {
							delete(sameGuildIntervals, config.Guilds[guildsIndex])
						}
					}
				}

				snipe(session.Vanities[event.ID], session.Token, 0)

				session.Vanities[event.ID] = event.VanityURLCode
			}
		}

		if data.Type == "GUILD_DELETE" {
			event := Guild{}

			if err = json.Unmarshal(data.RawData, &event); err != nil {
				logger.Errorf("Failed to unmarshal GUILD_CREATE payload: %v", err)
				return err
			}

			if event.VanityURLCode == "" {
				return nil
			}

			if config.IgnoreHostGuilds && slices.Contains(config.Guilds, event.ID) {
				return nil
			}

			logger.Infof("Guild %v was deleted. The vanity may be free: %v", guilds[event.ID].Name, event.VanityURLCode)
			if config.SameGuildTimeout != 0 {
				interval, existing := sameGuildIntervals[config.Guilds[guildsIndex]]

				if existing {
					difference := time.Until(*interval)

					if difference > 0 {
						logger.Warnf("Guild %v is on timeout for %.2fs. Ignoring guild deletion.", config.Guilds[guildsIndex], difference.Seconds())
						return nil
					} else {
						delete(sameGuildIntervals, config.Guilds[guildsIndex])
					}
				}
			}

			snipe(event.VanityURLCode, session.Token, 0)
		}

		if data.Type == "RESUMED" {
			logger.Infof("Logged in by resuming old session with %v guilds", len(guilds))
		}
	}

	if data.Operation == HEARTBEAT {
		logger.Debugf("Sending heartbeat in response to heartbeat (Sequence: %v)", session.sequence)

		err = session.websocket.WriteJSON(HeartbeatSendEvent{1, atomic.LoadInt64(session.sequence)})

		if err != nil {
			logger.Debugf("Failed to send heartbeat: %v", err)
			return err
		}

		session.LastHeartbeatSent = time.Now().UTC()

		return nil
	}

	if data.Operation == HELLO {
		event := HelloEvent{}

		if err = json.Unmarshal(data.RawData, &event); err != nil {
			logger.Errorf("Failed to unmarshal HELLO payload: %v", err)
			return err
		}

		milliseconds := event.HeartbeatInterval * time.Millisecond
		session.heartbeatInterval = &milliseconds

		sequence := atomic.LoadInt64(session.sequence)

		session.LastHeartbeatAck = time.Now().UTC()

		if session.sessionID == "" && sequence == 0 {
			err = session.identify()

			if err != nil {
				logger.Errorf("Failed to identify to gateway: %v", err)
				return err
			}
		} else {
			err = session.resume(sequence)

			if err != nil {
				logger.Errorf("Failed to resume session: %v. Got error: %s", session.gateway, err)
				return err
			}
		}

		go session.heartbeat()
	}

	if data.Operation == HEARTBEAT_ACK {
		session.LastHeartbeatAck = time.Now().UTC()
		logger.Debugf("Heartbeat acknowledged.")
	}

	return nil
}

func (session *Session) heartbeat() {
	if session.websocket == nil {
		return
	}

	var err error
	ticker := time.NewTicker(*session.heartbeatInterval)
	defer ticker.Stop()

	for {
		if session.State == "CLOSED" || session.State == "CLOSING" {
			return
		}

		lastHeartbeat := session.LastHeartbeatAck
		sequence := atomic.LoadInt64(session.sequence)

		logger.Debugf("Sending gateway websocket heartbeat (Sequence: %d)", sequence)

		session.LastHeartbeatSent = time.Now().UTC()
		payload := HeartbeatSendEvent{1, sequence}
		err = session.websocket.WriteJSON(payload)
		atomic.AddInt64(session.sequence, 1)

		if err != nil || time.Now().UTC().Sub(lastHeartbeat) > ((*session.heartbeatInterval)*(FAILED_HEARTBEAT_ACKS)) {
			if err != nil {
				logger.Debugf("Encountered error while sending heartbeat: %s, attempting to reconnect.", err)
			} else {
				logger.Debugf("Haven't received a heartbeat ACK in %v, attempting to reconnect.", time.Now().UTC().Sub(lastHeartbeat))
			}

			session.Close()
			session.reconnect()
			return
		}

		select {
		case <-session.C:
			return
		case <-ticker.C:
			// continue loop and send heartbeat
		}
	}
}

func (session *Session) reconnect() {
	var err error

	if session.ShouldReconnectOnError {
		wait := time.Duration(1)

		for {
			logger.Info("Attempting to reconnect to gateway.")

			err = session.Connect()
			if err == nil {
				logger.Info("Successfully reconnected to gateway.")
				return
			}

			if err == ErrWebsocketAlreadyConnected {
				logger.Info("WebSocket already exists, no need to reconnect")
				return
			}

			logger.Errorf("Failed to reconnect to gateway: %v", err)

			<-time.After(wait * time.Second)
			wait *= 2

			if wait > 600 {
				wait = 600
			}
		}
	}
}

func (session *Session) resume(sequence int64) error {
	session.State = "RESUMING"

	payload := ResumeEvent{}

	payload.Op = 6
	payload.Data.Token = session.Token
	payload.Data.SessionID = session.sessionID
	payload.Data.Sequence = sequence

	logger.Infof("Attempting to resume session: %v", session.sessionID)

	err := session.websocket.WriteJSON(payload)

	if err != nil {
		logger.Errorf("Failed to resume session: %v. Got error: %s", session.gateway, err)
		return err
	}

	return nil
}

func (session *Session) identify() error {
	session.State = "IDENTIFYING"

	atomic.StoreInt64(session.sequence, 0)
	session.sessionID = ""

	payload := IdentifyEvent{}
	payload.Op = 2
	payload.Data = session.Identify

	logger.Debug("Attempting to identify to gateway.")
	err := session.websocket.WriteJSON(payload)

	if err != nil {
		logger.Errorf("Failed to identify: %v. Got error: %s", session.gateway, err)
		return err
	}

	logger.Debug("Identified to gateway.")

	return nil
}

type RatelimitedResponse struct {
	RetryAfter float64 `json:"retry_after,omitempty"`
	Message    string  `json:"message,omitempty"`
	Code       string  `json:"code,omitempty"`
}

type CodeResponse struct {
	Uses int    `json:"uses,omitempty"`
	Code string `json:"code,omitempty"`
}

type FailedResponse struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func snipe(vanity string, token string, tries int) {
	logger.Infof("Attempting to snipe vanity: %v", vanity)

	payload, err := json.Marshal(map[string]string{"code": vanity})

	if err != nil {
		logger.Fatalf("Failed to marshall code: %v", err)
	}

	client := &http.Client{}
	guild := config.Guilds[guildsIndex]
	url := fmt.Sprintf("https://discord.com/api/v%v/guilds/%v/vanity-url", config.APIVersion, guild)
	request, err := http.NewRequest("PATCH", url, bytes.NewBuffer(payload))

	if err != nil {
		logger.Errorf("Failed to make request: %v", err)
		return
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", token)
	request.Header.Set("X-Super-Properties", superProperties)
	request.Header.Set("User-Agent", config.Properties.UserAgent)

	start := time.Now()
	res, err := client.Do(request)
	elapsed := time.Since(start)

	if err != nil {
		logger.Errorf("Failed to complete request: %v", err)
		return
	}

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)

	if err != nil {
		logger.Errorf("Failed to decode body: %v", err)
		return
	}

	if res.StatusCode == 429 {
		jsonBody := RatelimitedResponse{}
		err := json.Unmarshal([]byte(body), &jsonBody)

		if err != nil {
			logger.Errorf("Failed to unmarshall body: %v", err)
		}

		ratelimit := time.Duration(jsonBody.RetryAfter) * time.Second

		logger.Warnf("Ratelimited for %v while trying to snipe vanity: %v (Time: %.2fs)", ratelimit, vanity, elapsed.Seconds())
		time.Sleep(ratelimit)

		if config.Retries > tries {
			tries += 1
			logger.Infof("Retrying snipe for vanity: %v (Retry: #%v)", vanity, tries)
			snipe(vanity, token, tries)
			return
		} else {
			logger.Infof("Failed sniping %v after %v attempts.", vanity, config.Retries)
		}

		return
	}

	if res.StatusCode == 400 {
		jsonBody := FailedResponse{}
		err := json.Unmarshal([]byte(body), &jsonBody)

		if err != nil {
			logger.Errorf("Failed to unmarshall body: %v", err)
		}

		logger.Warnf("Failed to snipe vanity: %v (Reason: %v, Time: %.2fs)", vanity, jsonBody.Message, elapsed.Seconds())
		return
	}

	if res.StatusCode == 200 {
		jsonBody := CodeResponse{}
		err := json.Unmarshal([]byte(body), &jsonBody)

		if err != nil {
			logger.Errorf("Failed to unmarshall body: %v", err)
		}

		logger.Infof("Successfully sniped vanity: %v to guild %v (%.2fs)", vanity, guild, elapsed.Seconds())
		guildsIndex += 1

		sendToWebhook(fmt.Sprintf("Successfully sniped vanity: %v to guild %v (%.2fs)", vanity, guild, elapsed.Seconds()))

		if config.SameGuildTimeout != 0 {
			date := time.Now().Add(time.Duration(config.SameGuildTimeout) * time.Millisecond)
			sameGuildIntervals[guild] = &date
		}

		if guildsIndex >= (len(config.Guilds) - 1) {
			if config.RotateGuilds {
				logger.Warnf("Used up all available guilds for vanity sniping. As rotate guilds is turned on, we will re-use them in order.")
				guildsIndex = 0
			} else {
				logger.Warnf("Ran out of guilds to use. as config.rotateGuilds is turned off, the process will now exit.")
				exit()
			}
		}
	}

	logger.Warnf("Got unknown response code. (Status: %v, Body: %v)", res.StatusCode, string(body))
}
