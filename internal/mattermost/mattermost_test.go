package mattermost

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestUsernameAcceptsWhatPeopleType(t *testing.T) {
	cases := map[string]string{
		"anna.andersson":      "anna.andersson",
		"@anna.andersson":     "anna.andersson",
		"  @Anna.Andersson  ": "anna.andersson",
		"https://chat.rudbeckia.nu/rudbeckia/messages/@anna.andersson": "anna.andersson",
		"": "",
	}
	for in, want := range cases {
		if got := Username(in); got != want {
			t.Errorf("Username(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisplayNameFallsBackFromRealNameToUsername(t *testing.T) {
	cases := []struct {
		user User
		want string
	}{
		{User{Username: "anna", FirstName: "Anna", LastName: "Andersson"}, "Anna Andersson"},
		{User{Username: "anna", FirstName: "Anna"}, "Anna"},
		{User{Username: "anna", Nickname: "Annis"}, "Annis"},
		{User{Username: "anna"}, "anna"},
	}
	for _, c := range cases {
		if got := c.user.DisplayName(); got != c.want {
			t.Errorf("DisplayName(%+v) = %q, want %q", c.user, got, c.want)
		}
	}
}

// Without a server the client must stay usable: lookups answer with the name
// as typed, and messages are logged rather than lost or fatal.
func TestDisabledClientDegradesGracefully(t *testing.T) {
	c := New("", "", quiet())
	if c.Enabled() {
		t.Fatal("a client without url or token must report itself disabled")
	}
	u, err := c.ByUsername(context.Background(), "@Anna.Andersson")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if u.Username != "anna.andersson" || u.ID != "" {
		t.Errorf("lookup = %+v, want the typed name and no id", u)
	}
	if users, err := c.Search(context.Background(), "anna"); err != nil || users != nil {
		t.Errorf("search = %v, %v; want nobody and no error", users, err)
	}
	if err := c.DM(context.Background(), "u-anna", "hej"); err != nil {
		t.Errorf("DM on a disabled client should be a no-op, got %v", err)
	}
}

func TestSearchSkipsBotsAndDeactivatedAccounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/search" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("unexpected request: %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode([]User{
			{ID: "1", Username: "anna"},
			{ID: "2", Username: "gammal", DeleteAt: 12345},
			{ID: "3", Username: "annan-bot", IsBot: true},
		})
	}))
	defer srv.Close()

	users, err := New(srv.URL, "tok", quiet()).Search(context.Background(), "ann")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(users) != 1 || users[0].Username != "anna" {
		t.Errorf("search returned %+v, want only the active person", users)
	}
}

func TestByUsernameRefusesDeactivatedAccounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(User{ID: "2", Username: "gammal", DeleteAt: 12345})
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "tok", quiet()).ByUsername(context.Background(), "gammal"); err == nil {
		t.Error("a deactivated account must not resolve to a bookable member")
	}
}

func TestDMOpensADirectChannelAndUploadsTheAttachment(t *testing.T) {
	var (
		channelFor []string
		posted     struct {
			ChannelID string   `json:"channel_id"`
			Message   string   `json:"message"`
			FileIDs   []string `json:"file_ids"`
		}
		uploaded string
	)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(User{ID: "bot", Username: "booking", IsBot: true})
	})
	mux.HandleFunc("POST /api/v4/channels/direct", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&channelFor)
		json.NewEncoder(w).Encode(map[string]string{"id": "chan"})
	})
	mux.HandleFunc("POST /api/v4/files", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse upload: %v", err)
		}
		uploaded = r.MultipartForm.File["files"][0].Filename
		json.NewEncoder(w).Encode(map[string]any{
			"file_infos": []map[string]string{{"id": "f1"}},
		})
	})
	mux.HandleFunc("POST /api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&posted)
		json.NewEncoder(w).Encode(map[string]string{"id": "p1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "tok", quiet())
	err := c.DM(context.Background(), "u-anna", "Hej!",
		File{Filename: "ellastcykel.ics", Data: []byte("BEGIN:VCALENDAR")})
	if err != nil {
		t.Fatalf("DM: %v", err)
	}
	if len(channelFor) != 2 || channelFor[0] != "bot" || channelFor[1] != "u-anna" {
		t.Errorf("direct channel opened for %v, want [bot u-anna]", channelFor)
	}
	if uploaded != "ellastcykel.ics" {
		t.Errorf("uploaded %q, want the calendar file", uploaded)
	}
	if posted.ChannelID != "chan" || posted.Message != "Hej!" {
		t.Errorf("posted %+v", posted)
	}
	if len(posted.FileIDs) != 1 || posted.FileIDs[0] != "f1" {
		t.Errorf("the post should reference the uploaded file, got %v", posted.FileIDs)
	}
}

// A failed upload must not swallow the message itself.
func TestDMStillSendsWhenTheAttachmentFails(t *testing.T) {
	var posted struct {
		Message string   `json:"message"`
		FileIDs []string `json:"file_ids"`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v4/users/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(User{ID: "bot", Username: "booking", IsBot: true})
	})
	mux.HandleFunc("POST /api/v4/channels/direct", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "chan"})
	})
	mux.HandleFunc("POST /api/v4/files", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"file uploads are disabled"}`, http.StatusForbidden)
	})
	mux.HandleFunc("POST /api/v4/posts", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&posted)
		json.NewEncoder(w).Encode(map[string]string{"id": "p1"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "tok", quiet())
	if err := c.DM(context.Background(), "u-anna", "Hej!", File{Filename: "x.ics", Data: []byte("x")}); err != nil {
		t.Fatalf("DM: %v", err)
	}
	if posted.Message != "Hej!" || len(posted.FileIDs) != 0 {
		t.Errorf("posted %+v, want the message without attachments", posted)
	}
}

func TestErrorsCarryTheServersExplanation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"id":"api.context.session_expired","message":"Invalid or expired session"}`,
			http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "stale", quiet()).Verify(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Invalid or expired session") {
		t.Errorf("error = %v, want the server's own message", err)
	}
}

func TestFoldMakesAccentsAndCapitalsIrrelevant(t *testing.T) {
	cases := map[string]string{
		"Östberg":      "ostberg",
		"ÖSTBERG":      "ostberg",
		"Åsa Ängström": "asa angstrom",
		"  Mikael  ":   "mikael",
		"Müller":       "muller",
		"":             "",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDirectoryPagesThroughActivePeople(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		if r.URL.Query().Get("per_page") != "200" {
			t.Errorf("per_page = %q, want the largest page", r.URL.Query().Get("per_page"))
		}
		// A first page that is full, then a short one to stop on.
		var batch []User
		if r.URL.Query().Get("page") == "0" {
			for i := 0; i < 200; i++ {
				batch = append(batch, User{ID: "u", Username: "medlem"})
			}
			batch[7] = User{ID: "gone", Username: "gammal", DeleteAt: 1}
			batch[9] = User{ID: "bot", Username: "annan-bot", IsBot: true}
		} else {
			batch = []User{{ID: "last", Username: "sist"}}
		}
		json.NewEncoder(w).Encode(batch)
	}))
	defer srv.Close()

	users, truncated, err := New(srv.URL, "tok", quiet()).Directory(context.Background())
	if err != nil {
		t.Fatalf("directory: %v", err)
	}
	if truncated {
		t.Error("201 accounts fit well inside the limit")
	}
	if len(pages) != 2 || pages[0] != "0" || pages[1] != "1" {
		t.Errorf("asked for pages %v, want 0 then 1", pages)
	}
	// 200 on the first page less the deactivated account and the bot, plus one.
	if len(users) != 199 {
		t.Errorf("got %d people, want 199 (no bots, no deactivated accounts)", len(users))
	}
	if users[len(users)-1].Username != "sist" {
		t.Errorf("the last page was dropped: %+v", users[len(users)-1])
	}
}

// A directory larger than the picker can hold must say so, not quietly offer
// half the house.
func TestDirectoryReportsWhenItIsCutShort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batch := make([]User, 0, directoryPage)
		for i := 0; i < directoryPage; i++ {
			batch = append(batch, User{ID: "u", Username: "medlem"})
		}
		json.NewEncoder(w).Encode(batch)
	}))
	defer srv.Close()

	users, truncated, err := New(srv.URL, "tok", quiet()).Directory(context.Background())
	if err != nil {
		t.Fatalf("directory: %v", err)
	}
	if !truncated {
		t.Error("a server that never runs out of pages must report a truncated listing")
	}
	if len(users) != DirectoryLimit {
		t.Errorf("got %d people, want the limit of %d", len(users), DirectoryLimit)
	}
}

func TestDisabledClientHasNoDirectory(t *testing.T) {
	users, truncated, err := New("", "", quiet()).Directory(context.Background())
	if err != nil || truncated || users != nil {
		t.Errorf("directory on a disabled client = %v, %v, %v; want nothing", users, truncated, err)
	}
}
