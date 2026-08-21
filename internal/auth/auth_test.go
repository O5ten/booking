package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func guard() *Guard {
	return New("husets-losenord", "admin-losenord", []byte("test-secret-32-bytes-long-------"), time.Hour, false)
}

func TestCheckDistinguishesRoles(t *testing.T) {
	g := guard()
	if got := g.Check("husets-losenord"); got != RoleMember {
		t.Errorf("member password gave %q", got)
	}
	if got := g.Check("admin-losenord"); got != RoleAdmin {
		t.Errorf("admin password gave %q", got)
	}
	for _, bad := range []string{"", "fel", "husets-losenord ", "HUSETS-LOSENORD"} {
		if got := g.Check(bad); got.LoggedIn() {
			t.Errorf("Check(%q) = %q, want no access", bad, got)
		}
	}
}

func TestSessionRoundTrip(t *testing.T) {
	g := guard()
	for _, role := range []Role{RoleMember, RoleAdmin} {
		rec := httptest.NewRecorder()
		g.Issue(rec, role)

		req := httptest.NewRequest("GET", "/", nil)
		for _, c := range rec.Result().Cookies() {
			req.AddCookie(c)
		}
		if got := g.Role(req); got != role {
			t.Errorf("round-tripped %q as %q", role, got)
		}
	}
}

func TestSessionRejectsTamperedCookie(t *testing.T) {
	g := guard()
	rec := httptest.NewRecorder()
	g.Issue(rec, RoleMember)
	cookie := rec.Result().Cookies()[0]

	// Promoting member to admin by editing the cookie must fail the signature.
	tampered := *cookie
	tampered.Value = strings.Replace(cookie.Value, "member", "admin", 1)

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&tampered)
	if got := g.Role(req); got != RoleNone {
		t.Errorf("tampered cookie granted %q", got)
	}

	// A cookie signed with a different secret is rejected too.
	other := New("husets-losenord", "admin-losenord", []byte("a-completely-different-secret--"), time.Hour, false)
	rec2 := httptest.NewRecorder()
	other.Issue(rec2, RoleAdmin)
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(rec2.Result().Cookies()[0])
	if got := g.Role(req2); got != RoleNone {
		t.Errorf("foreign signature granted %q", got)
	}
}

func TestSessionExpires(t *testing.T) {
	g := New("pw", "", []byte("secret"), -time.Minute, false)
	rec := httptest.NewRecorder()
	g.Issue(rec, RoleMember)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "rb_session", Value: rec.Result().Cookies()[0].Value})
	if got := g.Role(req); got != RoleNone {
		t.Errorf("expired session granted %q", got)
	}
}

// Removing ADMIN_PASSWORD must void any admin session already out there.
func TestAdminSessionDiesWithTheAdminPassword(t *testing.T) {
	g := guard()
	rec := httptest.NewRecorder()
	g.Issue(rec, RoleAdmin)
	cookie := rec.Result().Cookies()[0]

	without := New("husets-losenord", "", []byte("test-secret-32-bytes-long-------"), time.Hour, false)
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if got := without.Role(req); got != RoleNone {
		t.Errorf("admin session survived removing the admin password: %q", got)
	}
	if without.HasAdmin() {
		t.Error("HasAdmin should be false without an admin password")
	}
}

func TestClearRemovesTheSession(t *testing.T) {
	g := guard()
	rec := httptest.NewRecorder()
	g.Clear(rec)
	c := rec.Result().Cookies()[0]
	if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("clear cookie = %+v, want an immediate expiry", c)
	}
}

func TestIdentityRoundTripAndTamper(t *testing.T) {
	g := guard()
	want := Identity{Name: "Anna Andersson", Apartment: "1403",
		MMUsername: "anna.andersson", MMUserID: "e3pn8o34qpb3if7z49gxum59oy", Phone: "070-1234567"}

	rec := httptest.NewRecorder()
	g.RememberIdentity(rec, want)
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	if got := g.Identity(req); got != want {
		t.Errorf("identity round-tripped as %+v, want %+v", got, want)
	}

	bad := httptest.NewRequest("GET", "/", nil)
	bad.AddCookie(&http.Cookie{Name: "rb_ident", Value: "garbage.123.xyz"})
	if got := g.Identity(bad); !got.Empty() {
		t.Errorf("garbage cookie produced %+v", got)
	}

	none := httptest.NewRequest("GET", "/", nil)
	if got := g.Identity(none); !got.Empty() {
		t.Errorf("missing cookie produced %+v", got)
	}
}

func TestTokensAreUniqueAndURLSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		for _, s := range []string{Token(), ID()} {
			if seen[s] {
				t.Fatalf("duplicate token %q after %d rounds", s, i)
			}
			seen[s] = true
			if strings.ContainsAny(s, "+/=?&# ") {
				t.Fatalf("token %q is not URL safe", s)
			}
		}
	}
}

func TestRoleHelpers(t *testing.T) {
	if !RoleAdmin.Admin() || RoleMember.Admin() || RoleNone.Admin() {
		t.Error("Admin() is wrong")
	}
	if !RoleAdmin.LoggedIn() || !RoleMember.LoggedIn() || RoleNone.LoggedIn() {
		t.Error("LoggedIn() is wrong")
	}
}
