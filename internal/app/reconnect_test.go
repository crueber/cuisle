package app_test

import "testing"

// Regression (found driving the real stack): Connect after Disconnect on the same site must succeed and keep data.
func TestReconnectAfterDisconnect_SameSite(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	if err := ta.Svc.Connection.Disconnect(ctx, false); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	c, err := ta.Svc.Connection.Connect(ctx, ta.connectInput())
	if err != nil {
		t.Fatalf("Connect after Disconnect: %v (%#v)", err, err)
	}
	if c.State != "ok" {
		t.Fatalf("state = %q, want ok", c.State)
	}
	if v := ta.view(id); len(v.Tickets) == 0 {
		t.Fatalf("data lost after reconnect")
	}
}
