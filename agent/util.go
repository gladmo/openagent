package agent

import (
	"encoding/json"
	"net/http"
	"time"
)

func jsonDecode(response *http.Response, out any) error {
	return json.NewDecoder(response.Body).Decode(out)
}

func timeNowUnixMilli() int64 { return time.Now().UnixMilli() }
