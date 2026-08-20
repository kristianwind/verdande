package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A subscription must not be able to point the server at its own network.
//
// The endpoint is whatever the browser handed over, which is to say whatever the
// person signed in handed over, and this server POSTs to it on a schedule they
// choose. The subscribe handler refuses a private address when it is written
// down — and that check runs on the URL as written, so a name it approves can
// answer 127.0.0.1 the next time it is looked up. This is the other half: the
// address that will actually be dialled, checked at the moment of dialling.
func TestSendRefusesToReachInside(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	// httptest listens on 127.0.0.1, which is exactly the destination in question.
	if !strings.Contains(srv.URL, "127.0.0.1") {
		t.Skipf("the test server is not on loopback: %s", srv.URL)
	}

	err := Send(context.Background(), testSubscription(t, srv.URL), Payload{Title: "hej"}, testVAPID(t))
	if err == nil {
		t.Fatal("Send reached a loopback address without complaining")
	}
	if reached {
		t.Error("the request arrived: the wall let it through and only the answer failed")
	}
}

func testSubscription(t *testing.T, endpoint string) Subscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return Subscription{
		Endpoint: endpoint,
		P256dh:   b64.EncodeToString(key.PublicKey().Bytes()),
		Auth:     b64.EncodeToString(auth),
	}
}

func testVAPID(t *testing.T) VAPID {
	t.Helper()
	pub, priv, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	return VAPID{Public: pub, Private: priv, Subject: "mailto:kristian@example.dk"}
}
