package compute

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDialInPinnedTLS(t *testing.T) {
	cert, fp, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(DialInHandler("s3cret", Runner{Home: t.TempDir()}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	defer srv.Close()

	c := Compute{Mode: ModeDialIn, URL: srv.URL, DialSecret: "s3cret", PinSHA256: strings.ToUpper(fp)}
	if _, err := DialInfo(context.Background(), c); err != nil {
		t.Fatalf("right pin: %v", err)
	}
	c.PinSHA256 = strings.Repeat("0", 64)
	if _, err := DialInfo(context.Background(), c); err == nil {
		t.Fatal("a wrong pin must fail")
	}
	c.PinSHA256 = ""
	if _, err := DialInfo(context.Background(), c); err == nil {
		t.Fatal("an unpinned self-signed certificate must fail")
	}
}
