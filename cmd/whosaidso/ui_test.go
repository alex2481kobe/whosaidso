package main

// The viewer serves the read verbs' own answers: every /api/view response is
// byte for byte what `whosaidso VIEW --json` prints for the same project, and
// the project list carries todo's own watermark and totals.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/ui"
)

func uiGet(t *testing.T, address, token, path string) (int, string) {
	t.Helper()
	u, _ := url.Parse(address)
	req, err := http.NewRequest(http.MethodGet, "http://"+u.Host+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-WhoSaidSo-Token", token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// withoutObservedAt drops continue's fresh clock reading, the one value two
// runs of the same view cannot share.
func withoutObservedAt(t *testing.T, text string) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, text)
	}
	if observed, ok := v["observed"].(map[string]any); ok {
		delete(observed, "observed_at")
	}
	out, _ := json.Marshal(v)
	return string(out)
}

func TestViewerServesExactlyTheCLIJSON(t *testing.T) {
	root, data := cliFixture(t)
	cliControl(t, root, data)
	ln, server, address, err := ui.Listen(uiConfig(root))
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(ln)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	u, _ := url.Parse(address)
	token := u.Query().Get("token")
	project := url.QueryEscape("test/cli")
	id := string(cliID(1))

	for _, c := range []struct {
		query string
		args  []string
	}{
		{"view=todo", []string{"todo", "--json"}},
		{"view=show", []string{"show", "--json"}},
		{"view=show&stale=1", []string{"show", "--stale", "--json"}},
		{"view=show&id=" + id, []string{"show", id, "--json"}},
		{"view=show&id=" + id + "&stale=1", []string{"show", id, "--stale", "--json"}},
		{"view=history", []string{"history", "--json"}},
		{"view=history&id=" + id, []string{"history", id, "--json"}},
		{"view=continue&id=" + id, []string{"continue", id, "--json"}},
		{"view=show&id=" + string(cliID(9)), []string{"show", string(cliID(9)), "--json"}},
	} {
		want, errs, code := cliRun(t, root, nil, "", c.args...)
		if code != 0 {
			t.Fatalf("control: %v exited %d: %s", c.args, code, errs)
		}
		status, got := uiGet(t, address, token, "/api/view?project="+project+"&"+c.query)
		if status != http.StatusOK {
			t.Fatalf("%s: status %d: %s", c.query, status, got)
		}
		if c.args[0] == "continue" {
			want, got = withoutObservedAt(t, want), withoutObservedAt(t, got)
		}
		if got != want {
			t.Errorf("/api/view?%s differs from whosaidso %s:\n got %s\nwant %s", c.query, strings.Join(c.args, " "), got, want)
		}
	}

	todo, _, _ := cliRun(t, root, nil, "", "todo", "--json")
	var answer struct {
		Watermark json.RawMessage `json:"watermark"`
		Totals    json.RawMessage `json:"totals"`
	}
	_ = json.Unmarshal([]byte(todo), &answer)
	status, body := uiGet(t, address, token, "/api/projects")
	var list struct {
		Current  string `json:"current"`
		Projects []struct {
			ID        string          `json:"id"`
			Available bool            `json:"available"`
			Watermark json.RawMessage `json:"watermark"`
			Totals    json.RawMessage `json:"totals"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(body), &list); status != http.StatusOK || err != nil || len(list.Projects) != 1 {
		t.Fatalf("projects: %d %v %s", status, err, body)
	}
	p := list.Projects[0]
	if list.Current != "test/cli" || p.ID != "test/cli" || !p.Available || !jsonEqual(p.Watermark, answer.Watermark) || !jsonEqual(p.Totals, answer.Totals) {
		t.Fatalf("the project list must carry todo's own watermark and totals:\n%s\ntodo %s %s", body, answer.Watermark, answer.Totals)
	}
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return string(ja) == string(jb)
}
