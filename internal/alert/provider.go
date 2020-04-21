package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Provider holds the information needed to post alerts
type Provider struct {
	URL      string
	Username string
	Channel  string
}

// Payload holds the channel and attachments
type Payload struct {
	Channel     string       `json:"channel"`
	Username    string       `json:"username"`
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment holds the markdown message body
type Attachment struct {
	Color      string   `json:"color"`
	AuthorName string   `json:"author_name"`
	Text       string   `json:"text"`
	MrkdwnIn   []string `json:"mrkdwn_in"`
}

// NewProvider validates the URL and returns a provider object
func NewProvider(providerType string, hookURL string, username string, channel string) (*Provider, error) {
	hook, err := url.ParseRequestURI(hookURL)
	if err != nil {
		return nil, fmt.Errorf("invalid hook URL %s", hookURL)
	}

	if providerType == "discord" {
		// https://birdie0.github.io/discord-webhooks-guide/other/slack_formatting.html
		if !strings.HasSuffix(hookURL, "/slack") {
			hook.Path = path.Join(hook.Path, "slack")
			hookURL = hook.String()
		}
	}

	if username == "" {
		return nil, errors.New("empty username")
	}

	if channel == "" {
		return nil, errors.New("empty channel")
	}

	return &Provider{
		Channel:  channel,
		URL:      hookURL,
		Username: username,
	}, nil
}

// Post message to the provider hook URL
func (s *Provider) Post(name string, namespace string, message string, severity string) error {
	payload := Payload{
		Channel:  s.Channel,
		Username: s.Username,
	}

	color := "good"
	if severity == "error" {
		color = "danger"
	}

	a := Attachment{
		Color:      color,
		AuthorName: fmt.Sprintf("%s/%s", namespace, name),
		Text:       message,
		MrkdwnIn:   []string{"text"},
	}

	payload.Attachments = []Attachment{a}

	err := postMessage(s.URL, payload)
	if err != nil {
		return fmt.Errorf("postMessage failed: %w", err)
	}
	return nil
}

func postMessage(address string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling notification payload failed: %w", err)
	}

	b := bytes.NewBuffer(data)

	req, err := http.NewRequest("POST", address, b)
	if err != nil {
		return fmt.Errorf("http.NewRequest failed: %w", err)
	}
	req.Header.Set("Content-type", "application/json")

	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()

	res, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("sending notification failed: %w", err)
	}

	defer res.Body.Close()
	statusCode := res.StatusCode
	if statusCode != 200 {
		body, _ := ioutil.ReadAll(res.Body)
		return fmt.Errorf("sending notification failed: %s", string(body))
	}

	return nil
}
