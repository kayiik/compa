package middleware

import "testing"

// Only the endpoints that authenticate themselves may skip the dashboard
// session; everything that lets the owner control a Compita must not.
func TestOnlySelfAuthenticatingComputePathsArePublic(t *testing.T) {
	for _, c := range []struct {
		method, path string
		public       bool
	}{
		{"POST", "/api/compute/enroll", true},
		{"POST", "/api/compute/poll", true},
		{"POST", "/api/compute/result", true},
		{"POST", "/api/compute/peer", true},
		{"POST", "/api/compute/approval", true},
		{"POST", "/api/compute/events", true},
		{"GET", "/api/compitas/ana/stream", false},
		{"POST", "/api/compitas/ana/trigger", true},
		{"GET", "/api/compute/install.sh", true},
		{"GET", "/api/compute/kernel", true},
		{"GET", "/api/compute/live/abc123", true},
		{"GET", "/api/compute/live/", false},
		{"GET", "/api/compute/live/a/b", false},
		{"POST", "/api/compute/live/abc123", false},
		{"GET", "/api/compitas/ana/live/ws", false},
		{"POST", "/api/compute/install.sh", false},
		{"GET", "/api/compute/state", false},
		{"GET", "/api/compute/poll", false},
		{"GET", "/api/compitas/ana/trigger", false},
		{"POST", "/api/compitas//trigger", false},
		{"POST", "/api/compitas/a/b/trigger", false},
		{"POST", "/api/compitas/ana/trigger/token", false},
		{"DELETE", "/api/compitas/ana/trigger", false},
		{"GET", "/api/compute/approvals", false},
		{"POST", "/api/compute/approvals/x", false},
		{"POST", "/api/compitas/ana/objective", false},
		{"POST", "/api/compitas/ana/objective/stop", false},
		{"POST", "/api/compitas/ana/schedules", false},
		{"GET", "/api/compute", false},
	} {
		if got := isPublicLauncherDashboardPath(c.method, c.path); got != c.public {
			t.Errorf("%s %s public = %v, want %v", c.method, c.path, got, c.public)
		}
	}
}
