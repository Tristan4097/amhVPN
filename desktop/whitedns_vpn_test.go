package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"whitevpn-desktop/internal/model"
	"whitevpn-desktop/internal/profiles"
)

func TestParseWhiteDNSVPNCustomFrontingIPs(t *testing.T) {
	ips, err := parseWhiteDNSVPNCustomFrontingIPs(" 104.16.0.10,104.16.0.11,104.16.0.10 ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ips, ",") != "104.16.0.10,104.16.0.11" {
		t.Fatalf("unexpected custom IPs: %#v", ips)
	}
	if _, err := parseWhiteDNSVPNCustomFrontingIPs("104.16.0.10 104.16.0.11"); err == nil {
		t.Fatal("expected whitespace-separated IPs to be rejected")
	}
	if _, err := parseWhiteDNSVPNCustomFrontingIPs("104.16.0.1,104.16.0.2,104.16.0.3,104.16.0.4,104.16.0.5,104.16.0.6,104.16.0.7,104.16.0.8,104.16.0.9,104.16.0.10"); err != nil {
		t.Fatalf("expected ten IPs to be accepted: %v", err)
	}
	if _, err := parseWhiteDNSVPNCustomFrontingIPs("104.16.0.1,104.16.0.2,104.16.0.3,104.16.0.4,104.16.0.5,104.16.0.6,104.16.0.7,104.16.0.8,104.16.0.9,104.16.0.10,104.16.0.11"); err == nil {
		t.Fatal("expected more than ten IPs to be rejected")
	}
}

func testWhiteDNSVPNFrontingProfile() model.V2RayProfile {
	return testWhiteDNSVPNProfile("white-fronting", "WhiteDNS Fronting", "origin.example.com")
}

func testWhiteDNSVPNProfile(id string, name string, server string) model.V2RayProfile {
	profile := model.DefaultV2RayProfile()
	profile.ID = id
	profile.Name = name
	profile.SubscriptionID = whiteDNSVPNSubscriptionID
	profile.Protocol = model.V2RayProtocolVLESS
	profile.Server = server
	profile.ServerPort = 443
	profile.UUID = "11111111-1111-1111-1111-111111111111"
	profile.Network = "ws"
	profile.TLS = true
	return profile
}

func firstWhiteDNSVPNOutbound(t *testing.T, config string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(config), &root); err != nil {
		t.Fatal(err)
	}
	outbounds := root["outbounds"].([]any)
	return outbounds[0].(map[string]any)
}

func whiteDNSVPNTestProfiles(profiles []model.V2RayProfile) []model.V2RayProfile {
	var out []model.V2RayProfile
	for _, profile := range profiles {
		if profile.SubscriptionID == whiteDNSVPNSubscriptionID {
			out = append(out, profile)
		}
	}
	return out
}

// The built-in catalogue's address is the app's, not the user's. It is a
// constant here and must never reach the state, because everything the user can
// see — the subscriptions list, a backup export, the state handed to the
// interface — is built from that.
// A first launch has to list the catalogue, not wait for a refresh to add it.
//
// It used to be created only by a successful catalogue refresh or by recording
// an error against one. So a fresh install showed an empty source picker on the
// Servers page and "0 sources" on the Subscriptions page, while the catalogue
// itself worked — the connect path defaults to its id whatever the list says.
// It survived a long time because anyone who had refreshed once never saw it
// again, and every developer machine had refreshed by the time anyone looked. A
// macOS user on a clean install found it.
func TestFirstLaunchListsTheCatalogue(t *testing.T) {
	dir := t.TempDir()
	store := profiles.NewStore(filepath.Join(dir, "state.json"))
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	app := &App{store: store, configDir: dir, state: state}
	app.ensureWhiteDNSVPNSubscriptionLocked()

	listed := app.GetAppState().V2RaySubscriptions
	if len(listed) != 1 {
		t.Fatalf("a first launch should list the catalogue and nothing else, got %#v", listed)
	}
	if listed[0].ID != whiteDNSVPNSubscriptionID {
		t.Fatalf("the listed subscription is not the catalogue: %#v", listed[0])
	}
	if listed[0].Name != "amhVPN" {
		t.Fatalf("expected built-in display name amhvpn, got %q", listed[0].Name)
	}
	// Listing it must not start storing its address; see the test below.
	if listed[0].URL != "" {
		t.Fatalf("the catalogue's address was stored: %q", listed[0].URL)
	}
}

func TestBuiltInCatalogueAddressNeverEntersState(t *testing.T) {
	app := &App{state: model.DefaultAppState()}
	app.mu.Lock()
	idx := app.ensureWhiteDNSVPNSubscriptionLocked()
	app.mu.Unlock()

	if got := app.state.V2RaySubscriptions[idx].URL; got != "" {
		t.Fatalf("the catalogue address was stored: %q", got)
	}
	if app.state.V2RaySubscriptions[idx].ID != whiteDNSVPNSubscriptionID {
		t.Fatalf("expected the built-in subscription, got %#v", app.state.V2RaySubscriptions[idx])
	}

	raw, err := json.Marshal(app.state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), amhVPNSubscriptionURL) {
		t.Fatal("the catalogue address is reachable through the serialised state")
	}
}

// A state file written before that was true, or a restored backup, still has it.
func TestForgetBuiltInSubscriptionURLClearsAnOlderState(t *testing.T) {
	state := model.DefaultAppState()
	state.V2RaySubscriptions = []model.V2RaySubscription{
		{ID: whiteDNSVPNSubscriptionID, Name: whiteDNSVPNSubscriptionName, URL: amhVPNSubscriptionURL},
		{ID: "user-1", Name: "Mine", URL: "https://example.com/sub"},
	}

	next := forgetBuiltInSubscriptionURL(state)

	if next.V2RaySubscriptions[0].URL != "" {
		t.Fatalf("expected the built-in address to be dropped, got %q", next.V2RaySubscriptions[0].URL)
	}
	if next.V2RaySubscriptions[1].URL != "https://example.com/sub" {
		t.Fatalf("a subscription the user added is theirs and must be left alone, got %q", next.V2RaySubscriptions[1].URL)
	}
}

func TestBuiltInSubscriptionFetchesPlainLinksAndRefreshes(t *testing.T) {
	plain := testV2RaySubscriptionLink("amhvpn")
	fetches := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches++
		_, _ = w.Write([]byte(plain))
	}))
	defer server.Close()

	restore := amhVPNSubscriptionURL
	amhVPNSubscriptionURL = server.URL
	defer func() { amhVPNSubscriptionURL = restore }()

	app := testV2RaySubscriptionApp(t)
	app.mu.Lock()
	app.ensureWhiteDNSVPNSubscriptionLocked()
	app.state.V2RayProfiles = append(app.state.V2RayProfiles, duplicateTestV2RayProfile("manual", "Manual"))
	app.state.V2RaySubscriptions = append(app.state.V2RaySubscriptions, model.V2RaySubscription{ID: "user-sub", Name: "User", URL: "https://example.com/sub"})
	app.mu.Unlock()

	body, err := app.subscriptionBodyFor(t.Context(), model.BuiltInSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if body != plain {
		t.Fatalf("built-in body was changed instead of passed to the parser: %q", body)
	}
	if nodes, err := whiteVPNNodesFromSubscription(body); err != nil || len(nodes) != 1 {
		t.Fatalf("plain built-in subscription did not parse: nodes=%#v err=%v", nodes, err)
	}

	result, err := app.RefreshV2RaySubscription(model.BuiltInSubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Imported != 1 || result.Subscription.ImportedCount != 1 || result.Subscription.LastUpdatedAt == "" || result.Subscription.LastError != "" {
		t.Fatalf("unexpected built-in refresh result: %#v", result)
	}
	if fetches != 2 {
		t.Fatalf("expected the built-in URL to be fetched for each request, got %d fetches", fetches)
	}
	if result.Subscription.ID != model.BuiltInSubscriptionID || result.Subscription.Name != "amhVPN" || result.Subscription.URL != "" {
		t.Fatalf("unexpected built-in subscription after refresh: %#v", result.Subscription)
	}
	if !containsV2RayProfile(result.State.V2RayProfiles, "manual") {
		t.Fatalf("built-in refresh changed a manual profile: %#v", result.State.V2RayProfiles)
	}
	if user, ok := findV2RaySubscription(result.State, "user-sub"); !ok || user.URL != "https://example.com/sub" {
		t.Fatalf("built-in refresh changed a user subscription: %#v", result.State.V2RaySubscriptions)
	}
}

func TestBuiltInCatalogueRefusesEditAndDeletion(t *testing.T) {
	app := &App{state: model.DefaultAppState()}
	app.mu.Lock()
	app.ensureWhiteDNSVPNSubscriptionLocked()
	app.mu.Unlock()

	if _, err := app.SaveV2RaySubscription(model.V2RaySubscription{ID: whiteDNSVPNSubscriptionID, Name: "Mine", URL: "https://evil.example"}); err == nil {
		t.Fatal("expected editing the built-in catalogue to be refused")
	}
	if _, err := app.DeleteV2RaySubscription(whiteDNSVPNSubscriptionID); err == nil {
		t.Fatal("expected removing the built-in catalogue to be refused")
	}
	if _, ok := findV2RaySubscription(app.state, whiteDNSVPNSubscriptionID); !ok {
		t.Fatal("the built-in catalogue should still be listed")
	}
}

func TestPrivacyPolicyGateBlocksConnectingUntilAccepted(t *testing.T) {
	app := testV2RaySubscriptionApp(t)
	if privacyPolicyAccepted(app.GetAppState()) {
		t.Fatal("a fresh install has accepted nothing")
	}
	if _, err := app.StartWhiteDNSVPNConnection(); err == nil {
		t.Fatal("expected connecting to be refused before the policy is accepted")
	}

	if _, err := app.AcceptPrivacyPolicy(); err != nil {
		t.Fatal(err)
	}
	state := app.GetAppState()
	if state.WhiteVPN.AcceptedPrivacyPolicyVersion != model.CurrentPrivacyPolicyID {
		t.Fatalf("expected the current version to be recorded, got %d", state.WhiteVPN.AcceptedPrivacyPolicyVersion)
	}
	if !privacyPolicyAccepted(state) {
		t.Fatal("the gate should be satisfied once the current version is accepted")
	}
}

// A policy that changes brings the gate back; that is the point of versioning it.
func TestPrivacyPolicyGateReturnsForANewerVersion(t *testing.T) {
	state := model.DefaultAppState()
	state.WhiteVPN.AcceptedPrivacyPolicyVersion = model.CurrentPrivacyPolicyID - 1
	if privacyPolicyAccepted(state) {
		t.Fatal("an older acceptance must not satisfy the current policy")
	}
	state.WhiteVPN.AcceptedPrivacyPolicyVersion = model.CurrentPrivacyPolicyID + 1
	if !privacyPolicyAccepted(state) {
		t.Fatal("a state ahead of this build should not be asked again")
	}
}

func TestSelectSubscriptionDefaultsToTheBuiltInCatalogue(t *testing.T) {
	app := testV2RaySubscriptionApp(t)
	if got := app.selectedSubscriptionID(); got != whiteDNSVPNSubscriptionID {
		t.Fatalf("expected the built-in catalogue by default, got %q", got)
	}

	if _, err := app.SelectSubscription("does-not-exist"); err == nil {
		t.Fatal("expected selecting a subscription that is not listed to be refused")
	}
	if got := app.selectedSubscriptionID(); got != whiteDNSVPNSubscriptionID {
		t.Fatalf("a refused selection must change nothing, got %q", got)
	}
}

func TestSelectSubscriptionClearsANodePickedInAnotherList(t *testing.T) {
	app := testV2RaySubscriptionApp(t)
	id := addTestSubscription(t, app, "Mine", "https://example.com/sub")

	app.mu.Lock()
	app.state.WhiteVPN.Connection.Node = "a node from the old list"
	app.state.WhiteVPN.CountryCode = "DE"
	_, _ = app.saveLocked()
	app.mu.Unlock()
	app.storeWhiteVPNNodes(whiteDNSVPNSubscriptionID, []model.WhiteVPNNode{{Name: "cached"}}, testTime())

	state, err := app.SelectSubscription(id)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedSubscriptionID != id {
		t.Fatalf("expected the selection to be stored, got %q", state.SelectedSubscriptionID)
	}
	if state.WhiteVPN.Connection.Node != "" {
		t.Fatalf("a node named in the old list must not survive the change, got %q", state.WhiteVPN.Connection.Node)
	}
	if state.WhiteVPN.CountryCode != "DE" {
		t.Fatalf("a country filter is not tied to one list and should stay, got %q", state.WhiteVPN.CountryCode)
	}
	// Each subscription keeps its own catalogue, so the new selection starts
	// empty rather than inheriting the old one's nodes.
	if nodes := app.whiteVPNNodesSnapshot(id); len(nodes) != 0 {
		t.Fatalf("the newly selected subscription must not inherit another's nodes, got %#v", nodes)
	}
	if nodes := app.whiteVPNNodesSnapshot(whiteDNSVPNSubscriptionID); len(nodes) != 1 {
		t.Fatalf("the catalogue's own nodes should survive looking at another list, got %#v", nodes)
	}
}

// A selection pointing at a subscription that has been deleted must not leave
// the app with no source of servers.
func TestDeletingTheSelectedSubscriptionFallsBackToTheCatalogue(t *testing.T) {
	app := testV2RaySubscriptionApp(t)
	id := addTestSubscription(t, app, "Mine", "https://example.com/sub")
	if _, err := app.SelectSubscription(id); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteV2RaySubscription(id); err != nil {
		t.Fatal(err)
	}
	if got := app.selectedSubscriptionID(); got != whiteDNSVPNSubscriptionID {
		t.Fatalf("expected the built-in catalogue to be selected again, got %q", got)
	}
}

// http is allowed and marked rather than refused: a provider that serves one is
// a provider whose subscription has to be usable here. Anything that is not a
// web address is still refused.
func TestSubscriptionURLTakesWebAddressesAndNothingElse(t *testing.T) {
	for _, rawURL := range []string{"ftp://example.com/sub", "file:///tmp/sub", "", "not a url"} {
		if _, err := validateV2RaySubscriptionURL(rawURL); err == nil {
			t.Fatalf("expected %q to be rejected", rawURL)
		}
	}
	for _, rawURL := range []string{"https://example.com/sub", "http://sh.example.click:2096/sub/abc", "http://127.0.0.1:8080/sub"} {
		if _, err := validateV2RaySubscriptionURL(rawURL); err != nil {
			t.Fatalf("expected %q to be accepted: %v", rawURL, err)
		}
	}
}

func TestSubscriptionRedirectNeverDowngradesHTTPS(t *testing.T) {
	httpsURL, _ := url.Parse("https://example.com/sub")
	httpURL, _ := url.Parse("http://mirror.example.com/sub")
	if err := checkSubscriptionRedirect(&http.Request{URL: httpURL}, []*http.Request{{URL: httpsURL}}); err == nil {
		t.Fatal("HTTPS-to-HTTP redirect must be rejected")
	}
	if err := checkSubscriptionRedirect(&http.Request{URL: httpsURL}, []*http.Request{{URL: httpURL}}); err != nil {
		t.Fatalf("HTTP-to-HTTPS redirect should remain valid: %v", err)
	}
}

// The catalogue used to be stored as profiles so the Xray path could connect
// through one. Nothing fills that list now, so an older state file carries a
// frozen copy — and it was being counted and shown as though it were the
// catalogue, which is how the subscriptions page came to say 862 while the
// catalogue said 995.
func TestForgetBuiltInCatalogueProfilesKeepsWhatIsTheUsers(t *testing.T) {
	state := model.DefaultAppState()
	state.V2RayProfiles = []model.V2RayProfile{
		{ID: "stale-1", SubscriptionID: whiteDNSVPNSubscriptionID},
		{ID: "stale-2", SubscriptionID: whiteDNSVPNSubscriptionID},
		{ID: "mine", SubscriptionID: "user-1"},
		{ID: "hand-added"},
	}

	next := forgetBuiltInCatalogueProfiles(state)

	if len(next.V2RayProfiles) != 2 {
		t.Fatalf("expected the catalogue's copy to go and nothing else, got %#v", next.V2RayProfiles)
	}
	for _, profile := range next.V2RayProfiles {
		if profile.SubscriptionID == whiteDNSVPNSubscriptionID {
			t.Fatalf("a stale catalogue profile survived: %#v", profile)
		}
	}
}
