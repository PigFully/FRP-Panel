package panel

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The browser puts a resource id in the URL path, never in the body. Because
// decodeJSON rejects unknown fields, a body that also carries "id" turns every
// edit into a 400 — which is exactly what broke node edit, mapping edit and the
// mapping enable/disable toggle. These are the bodies web/src/api/hooks.ts
// actually sends, so this test fails if either side drifts again.
func TestDecodeJSONAcceptsFrontendBodies(t *testing.T) {
	cases := []struct {
		name string
		body string
		into any
	}{
		{"PUT /nodes/{id}", `{"name":"HK-01","region":"overseas"}`, new(updateNodeReq)},
		{"POST /nodes", `{"name":"HK-01","region":"overseas","receipt":"eyJpcCI6IjEuMi4zLjQifQ"}`, new(createNodeReq)},
		{
			"POST|PUT /mappings",
			`{"local_port":8090,"proto":"tcp","remark":"web","enabled":true,"version":3,` +
				`"targets":[{"node_id":1,"remote_port":18080},{"node_id":2,"remote_port":33223}]}`,
			new(mappingReq),
		},
		{"POST /mappings/{id}/toggle", `{"enabled":false,"version":4}`, new(toggleReq)},
		{
			"PUT /settings",
			`{"panel_name":"FRPanel","conn_rate_limit":200,"tcp_ping_interval":15,"auto_backup":true,"update_mirror":"https://ghproxy.example"}`,
			new(updateSettingsReq),
		},
		{"PUT /settings (partial)", `{"conn_rate_limit":0}`, new(updateSettingsReq)},
		{"POST /logs/clean", `{"all":true}`, new(cleanLogsReq)},
		{"POST /mappings/port-check", `{"node_id":1,"port":18080,"proto":"tcp"}`, new(portCheckReq)},
		{"POST /mappings/local-check", `{"port":8090,"proto":"tcp"}`, new(localCheckReq)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(c.body))
			if err := decodeJSON(r, c.into); err != nil {
				t.Fatalf("frontend body rejected: %v\nbody: %s", err, c.body)
			}
		})
	}
}

// A stray path id must be reported by name. The old blanket "请求体格式错误" gave
// the operator nothing to go on.
func TestDecodeJSONNamesUnknownField(t *testing.T) {
	r := httptest.NewRequest("PUT", "/", strings.NewReader(`{"id":7,"name":"HK-01","region":"overseas"}`))
	err := decodeJSON(r, new(updateNodeReq))
	if err == nil {
		t.Fatal("expected the unknown field to be rejected")
	}
	if !strings.Contains(err.Error(), `id`) {
		t.Errorf("error must name the offending field, got: %v", err)
	}
}

// Genuinely malformed bodies still get the generic message.
func TestDecodeJSONMalformed(t *testing.T) {
	r := httptest.NewRequest("PUT", "/", strings.NewReader(`{"name":`))
	err := decodeJSON(r, new(updateNodeReq))
	if err == nil {
		t.Fatal("expected a decode error")
	}
	if strings.Contains(err.Error(), "未知字段") {
		t.Errorf("truncated JSON is not an unknown-field error, got: %v", err)
	}
}

func TestUnknownField(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		found bool
	}{
		{`json: unknown field "id"`, "id", true},
		{`json: unknown field "local_port"`, "local_port", true},
		{"unexpected EOF", "", false},
		{`json: cannot unmarshal string into Go value of type int`, "", false},
		{`json: unknown field "unterminated`, "", false},
	}
	for _, c := range cases {
		got, ok := unknownField(errString(c.in))
		if ok != c.found || got != c.want {
			t.Errorf("unknownField(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.found)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
