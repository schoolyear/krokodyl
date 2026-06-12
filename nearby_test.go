package main

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// startTestServer spins up a real TLS nearby server on a loopback port with
// an auto-responder for prompts.
func startTestServer(t *testing.T, accept bool) (srv *nearbyServer, port int, fingerprint string, accepted *sync.Map) {
	t.Helper()
	accepted = &sync.Map{}

	var s *nearbyServer
	s, port, fingerprint, err := startNearbyServer(
		func(offer NearbyOffer) {
			// Simulate the user answering the prompt.
			go s.respond(offer.ID, accept)
		},
		func(senderName, code string) {
			accepted.Store("sender", senderName)
			accepted.Store("code", code)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s, port, fingerprint, accepted
}

func TestNearbyOfferAcceptHandsOverCode(t *testing.T) {
	_, port, fp, accepted := startTestServer(t, true)

	answer, err := sendNearbyOffer([]string{"127.0.0.1"}, port, fp, offerRequest{
		SenderName: "dev-box",
		Files:      []string{"build.zip"},
		Size:       1234,
	}, "9999-test-code-word")
	if err != nil {
		t.Fatal(err)
	}
	if !answer.Accepted {
		t.Fatal("offer should have been accepted")
	}

	// onAccept runs after the code message lands; allow a beat.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if code, ok := accepted.Load("code"); ok {
			if code != "9999-test-code-word" {
				t.Errorf("wrong code delivered: %v", code)
			}
			if sender, _ := accepted.Load("sender"); sender != "dev-box" {
				t.Errorf("wrong sender name: %v", sender)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("code never reached onAccept")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNearbyOfferDecline(t *testing.T) {
	_, port, fp, accepted := startTestServer(t, false)

	answer, err := sendNearbyOffer([]string{"127.0.0.1"}, port, fp, offerRequest{
		SenderName: "dev-box",
		Files:      []string{"a.txt"},
		Size:       1,
	}, "9999-test-code-word")
	if err != nil {
		t.Fatal(err)
	}
	if answer.Accepted {
		t.Fatal("offer should have been declined")
	}
	if _, ok := accepted.Load("code"); ok {
		t.Fatal("code must never be delivered on decline")
	}
}

func TestNearbyOfferWrongFingerprintRejected(t *testing.T) {
	_, port, _, _ := startTestServer(t, true)

	wrongFp := strings.Repeat("ab", 32)
	_, err := sendNearbyOffer([]string{"127.0.0.1"}, port, wrongFp, offerRequest{
		SenderName: "dev-box",
		Files:      []string{"a.txt"},
		Size:       1,
	}, "9999-test-code-word")
	if err == nil {
		t.Fatal("dial must fail when the certificate does not match the pinned fingerprint")
	}
}

func TestNearbyOfferEmptyFingerprintRejected(t *testing.T) {
	if _, err := sendNearbyOffer([]string{"127.0.0.1"}, 1, "", offerRequest{}, "x"); err == nil {
		t.Fatal("empty fingerprint must be rejected before dialing")
	}
}

func TestNearbyOfferTriesAllCandidates(t *testing.T) {
	_, port, fp, accepted := startTestServer(t, true)

	// First candidate is an unroutable test address; the offer must fall
	// through to the reachable loopback and still connect.
	answer, err := sendNearbyOffer([]string{"192.0.2.1", "127.0.0.1"}, port, fp, offerRequest{
		SenderName: "dev-box",
		Files:      []string{"build.zip"},
		Size:       1,
	}, "9999-test-code-word")
	if err != nil {
		t.Fatalf("should connect via the reachable candidate: %v", err)
	}
	if !answer.Accepted {
		t.Fatal("offer should have been accepted via the second candidate")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := accepted.Load("code"); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("code never reached onAccept")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNearbyOfferNoCandidates(t *testing.T) {
	if _, err := sendNearbyOffer(nil, 1, "abc", offerRequest{}, "x"); err == nil {
		t.Fatal("empty candidate list must error")
	}
}

func TestValidateOfferRequest(t *testing.T) {
	many := make([]string, maxOfferFiles+1)
	for i := range many {
		many[i] = "f"
	}

	tests := []struct {
		name    string
		req     offerRequest
		wantErr bool
	}{
		{"valid", offerRequest{SenderName: "a", Files: []string{"f"}, Size: 1}, false},
		{"no sender", offerRequest{Files: []string{"f"}}, true},
		{"no files", offerRequest{SenderName: "a"}, true},
		{"too many files", offerRequest{SenderName: "a", Files: many}, true},
		{"negative size", offerRequest{SenderName: "a", Files: []string{"f"}, Size: -1}, true},
		{"long sender", offerRequest{SenderName: strings.Repeat("x", 100), Files: []string{"f"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOfferRequest(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateOfferRequest(%+v) error = %v, wantErr %v", tt.req, err, tt.wantErr)
			}
		})
	}
}

func TestNearbyServerBusyWithSecondOffer(t *testing.T) {
	// First offer's prompt never answered (manual respond), second must get busy.
	var s *nearbyServer
	s, port, fp, err := func() (*nearbyServer, int, string, error) {
		return startNearbyServer(func(NearbyOffer) {}, func(string, string) {})
	}()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)

	firstDone := make(chan error, 1)
	go func() {
		_, err := sendNearbyOffer([]string{"127.0.0.1"}, port, fp, offerRequest{
			SenderName: "first", Files: []string{"a"}, Size: 1,
		}, "code-one")
		firstDone <- err
	}()

	// Give the first offer time to become pending.
	time.Sleep(300 * time.Millisecond)

	answer, err := sendNearbyOffer([]string{"127.0.0.1"}, port, fp, offerRequest{
		SenderName: "second", Files: []string{"b"}, Size: 1,
	}, "code-two")
	if err != nil {
		t.Fatal(err)
	}
	if answer.Accepted || !answer.Busy {
		t.Errorf("second offer should be busy-declined, got %+v", answer)
	}

	// Unblock the first offer.
	s.close()
	<-firstDone
}
