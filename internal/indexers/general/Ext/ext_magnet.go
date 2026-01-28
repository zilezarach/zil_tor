package Ext

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func FetchMagnet(
	client *http.Client,
	torrentID int,
	tokens *Tokens,
) (string, error) {
	ts := time.Now().Unix()
	hmac := ComputeHMAC(torrentID, ts, tokens.PageToken)

	form := url.Values{}
	form.Set("torrent_id", strconv.Itoa(torrentID))
	form.Set("action", "get_magnet")
	form.Set("timestamp", strconv.FormatInt(ts, 10))
	form.Set("hmac", hmac)
	form.Set("sessid", tokens.CsrfToken)

	req, err := http.NewRequest(
		"POST",
		"https://ext.to/ajax/getTorrentMagnet.php",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", err
	}

	// Set all required headers
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("Origin", "https://ext.to")
	req.Header.Set("Referer", fmt.Sprintf("https://ext.to/%d/", torrentID))
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:143.0) Gecko/20100101 Firefox/143.0")

	// Cookies are already in the client's jar
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var out struct {
		Success bool   `json:"success"`
		Magnet  string `json:"magnet"`
		Hash    string `json:"hash"`
		Error   string `json:"error"`
	}

	if err := json.Unmarshal(bodyBytes, &out); err != nil {
		return "", fmt.Errorf("failed to parse JSON: %w, body: %s", err, string(bodyBytes))
	}

	if !out.Success {
		if out.Error != "" {
			return "", errors.New(out.Error)
		}
		return "", fmt.Errorf("request failed: %s", string(bodyBytes))
	}

	if out.Magnet != "" {
		return out.Magnet, nil
	}

	if out.Hash != "" {
		return "magnet:?xt=urn:btih:" + out.Hash, nil
	}

	return "", errors.New("empty magnet and hash in response")
}
