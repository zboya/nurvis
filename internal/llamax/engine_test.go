package llamax

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEngine_Props(t *testing.T) {
	e := &Engine{
		baseURL: "http://localhost:8080",
	}
	resp, err := e.Props(context.Background())
	if err != nil {
		t.Error(err)
	}
	d, _ := json.Marshal(resp)
	t.Log(string(d))

}
