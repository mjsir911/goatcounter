package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"zgo.at/goatcounter/v2"
	"zgo.at/goatcounter/v2/gctest"
	"zgo.at/isbot"
	"zgo.at/zdb"
	"zgo.at/zstd/zcrypto"
	"zgo.at/zstd/zint"
	"zgo.at/zstd/zjson"
	"zgo.at/zstd/ztest"
	"zgo.at/zstd/ztime"
	"zgo.at/zstd/ztype"
)

func TestBackendCount(t *testing.T) {
	tests := []struct {
		name     string
		query    url.Values
		set      func(r *http.Request)
		wantCode int
		hit      goatcounter.Hit
	}{
		{"no path", url.Values{}, nil, 400, goatcounter.Hit{}},
		{"invalid size", url.Values{"p": {"/x"}, "s": {"xxx"}}, nil, 400, goatcounter.Hit{}},

		{"only path", url.Values{"p": {"/foo.html"}}, nil, 200, goatcounter.Hit{
			Path: "/foo.html",
		}},

		{"add slash", url.Values{"p": {"foo.html"}}, nil, 200, goatcounter.Hit{
			Path: "/foo.html",
		}},

		{"event", url.Values{"p": {"foo.html"}, "e": {"true"}}, nil, 200, goatcounter.Hit{
			Path:  "foo.html",
			Event: true,
		}},

		{"params", url.Values{"p": {"/foo.html?a=b&c=d"}}, nil, 200, goatcounter.Hit{
			Path: "/foo.html?a=b&c=d",
		}},

		{"ref", url.Values{"p": {"/foo.html"}, "r": {"https://example.com"}}, nil, 200, goatcounter.Hit{
			Path:      "/foo.html",
			Ref:       "example.com",
			RefScheme: ztype.Ptr("h"),
		}},

		{"str ref", url.Values{"p": {"/foo.html"}, "r": {"example"}}, nil, 200, goatcounter.Hit{
			Path:      "/foo.html",
			Ref:       "example",
			RefScheme: ztype.Ptr("o"),
		}},

		{"ref params", url.Values{"p": {"/foo.html"}, "r": {"https://example.com?p=x"}}, nil, 200, goatcounter.Hit{
			Path:      "/foo.html",
			Ref:       "example.com",
			RefScheme: ztype.Ptr("h"),
		}},

		{"full", url.Values{"p": {"/foo.html"}, "t": {"XX"}, "r": {"https://example.com?p=x"}, "s": {"40,50,1"}}, nil, 200, goatcounter.Hit{
			Path:      "/foo.html",
			Title:     "XX",
			Ref:       "example.com",
			RefScheme: ztype.Ptr("h"),
			Width:     ztype.Ptr(int16(40)),
		}},

		{"campaign", url.Values{"p": {"/foo.html"}, "q": {"ref=AAA"}}, nil, 200, goatcounter.Hit{
			Path:      "/foo.html",
			Ref:       "AAA",
			RefScheme: ztype.Ptr("c"),
		}},
		{"campaign_override", url.Values{"p": {"/foo.html?ref=AAA"}, "q": {"ref=AAA"}}, nil, 200, goatcounter.Hit{
			Path:      "/foo.html",
			Ref:       "AAA",
			RefScheme: ztype.Ptr("c"),
		}},

		{"width,height,dpr", url.Values{"p": {"/a"}, "s": {"1920,1080,2"}}, nil, 200, goatcounter.Hit{
			Path:  "/a",
			Width: ztype.Ptr(int16(1920)),
		}},
		{"width,height", url.Values{"p": {"/a"}, "s": {"1920,1080"}}, nil, 200, goatcounter.Hit{
			Path:  "/a",
			Width: ztype.Ptr(int16(1920)),
		}},
		{"width", url.Values{"p": {"/a"}, "s": {"1920"}}, nil, 200, goatcounter.Hit{
			Path:  "/a",
			Width: ztype.Ptr(int16(1920)),
		}},

		{"bot", url.Values{"p": {"/a"}, "b": {"150"}}, nil, 200, goatcounter.Hit{
			Path: "/a",
			Bot:  150,
		}},
		{"googlebot", url.Values{"p": {"/a"}, "b": {"150"}}, func(r *http.Request) {
			r.Header.Set("User-Agent", "GoogleBot/1.0")
		}, 200, goatcounter.Hit{
			Path:            "/a",
			Bot:             int(isbot.BotShort),
			UserAgentHeader: "GoogleBot/1.0",
		}},

		{"bot", url.Values{"p": {"/a"}, "b": {"100"}}, nil, 400, goatcounter.Hit{}},

		{"post", url.Values{"p": {"/foo.html"}}, func(r *http.Request) {
			r.Method = "POST"
		}, 200, goatcounter.Hit{
			Path: "/foo.html",
		}},

		{"long path", url.Values{"p": []string{"/" + strings.Repeat("a", 2047)}}, nil, 200, goatcounter.Hit{
			Path: "/" + strings.Repeat("a", 2047),
		}},
		{"too long", url.Values{"p": []string{"/" + strings.Repeat("a", 2048)}}, nil, 414, goatcounter.Hit{}},
		{"host", url.Values{"p": {"/foo.html"}, "h": {"www.example.org"}}, nil, 200, goatcounter.Hit{
			Path: "/foo.html", Host: "www.example.org",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := gctest.DB(t)
			ctx = ztime.WithNow(ctx, ztime.FromString("2019-06-18 14:42:00"))

			var site goatcounter.Site
			site.Defaults(ctx)

			site.CreatedAt = time.Date(2019, 01, 01, 0, 0, 0, 0, time.UTC)
			site.Settings.Collect.Set(goatcounter.CollectHits)
			ctx = gctest.Site(ctx, t, &site, nil)

			r, rr := newTest(ctx, "GET", "/count?"+tt.query.Encode(), nil)
			r.Host = site.Code + "." + goatcounter.Config(ctx).Domain
			if tt.set != nil {
				tt.set(r)
			}
			login(t, r)

			newBackend(zdb.MustGetDB(ctx)).ServeHTTP(rr, r)
			if h := rr.Header().Get("X-Goatcounter"); h != "" {
				t.Logf("X-Goatcounter: %s", h)
			}
			ztest.Code(t, rr, tt.wantCode)

			if tt.wantCode >= 400 {
				return
			}

			_, err := goatcounter.Memstore.Persist(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if tt.hit.UserAgentHeader == "" {
				tt.hit.UserAgentHeader = "GoatCounter test runner/1.0"
			}

			var hits goatcounter.Hits
			err = hits.TestList(ctx, false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(tt.name, "bot") {
				if len(hits) != 0 {
					t.Fatalf("len(hits) = %d: %#v", len(hits), hits)
				}
				have := zdb.DumpString(ctx, `select * from bots`, zdb.DumpVertical)
				want := ztest.NormalizeIndent(fmt.Sprintf(`
					site_id     2
					path        %s
					bot         %d
					user_agent  %s
					created_at  2019-06-18 14:42:00
				`, tt.hit.Path, tt.hit.Bot, tt.hit.UserAgentHeader))
				if d := ztest.Diff(have, want); d != "" {
					t.Error(d)
				}
				return
			}

			if len(hits) != 1 {
				t.Fatalf("len(hits) = %d: %#v", len(hits), hits)
			}

			h := hits[0]
			err = h.Validate(ctx, false)
			if err != nil {
				t.Errorf("Validate failed after get: %s", err)
			}

			tt.hit.ID = h.ID
			tt.hit.Site = h.Site
			tt.hit.CreatedAt = ztime.Now(ctx)
			tt.hit.Session = goatcounter.TestSeqSession // Should all be the same session.
			h.CreatedAt = h.CreatedAt.In(time.UTC)
			if d := ztest.Diff(string(zjson.MustMarshal(h)), string(zjson.MustMarshal(tt.hit)), ztest.DiffJSON); d != "" {
				t.Error(d)
			}
		})
	}
}

func TestBackendCountSessions(t *testing.T) {
	ctx := gctest.DB(t)
	ctx = ztime.WithNow(ctx, time.Date(2019, 6, 18, 14, 42, 0, 0, time.UTC))

	var set goatcounter.SiteSettings
	set.Defaults(ctx)
	set.Collect.Set(goatcounter.CollectHits)

	ctx1 := gctest.Site(ctx, t, &goatcounter.Site{
		CreatedAt: time.Date(2019, 01, 01, 0, 0, 0, 0, time.UTC),
		Settings:  set,
	}, nil)
	ctx2 := gctest.Site(ctx, t, &goatcounter.Site{
		CreatedAt: time.Date(2019, 01, 01, 0, 0, 0, 0, time.UTC),
		Settings:  set,
	}, nil)

	send := func(ctx context.Context, ua string) {
		site := Site(ctx)
		query := url.Values{"p": {"/" + zcrypto.Secret64()}}

		r, rr := newTest(ctx, "GET", "/count?"+query.Encode(), nil)
		r.Host = site.Code + "." + goatcounter.Config(ctx).Domain
		r.Header.Set("User-Agent", ua)
		newBackend(zdb.MustGetDB(ctx)).ServeHTTP(rr, r)
		if h := rr.Header().Get("X-Goatcounter"); h != "" {
			t.Logf("X-Goatcounter: %s", h)
		}
		ztest.Code(t, rr, 200)

		_, err := goatcounter.Memstore.Persist(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}

	checkHits := func(ctx context.Context, n int) []goatcounter.Hit {
		var hits goatcounter.Hits
		err := hits.TestList(ctx, true)
		if err != nil {
			t.Fatal(err)
		}

		if len(hits) != n {
			t.Errorf("len(hits) = %d; wanted %d", len(hits), n)
			for _, h := range hits {
				t.Logf("ID: %d; Site: %d; Session: %d\n", h.ID, h.Site, h.Session)
			}
			t.Fatal()
		}

		for _, h := range hits {
			err := h.Validate(ctx, false)
			if err != nil {
				t.Errorf("Validate failed after get: %s", err)
			}
		}
		return hits
	}

	checkSess := func(hits goatcounter.Hits, wantInt []int) {
		var got []zint.Uint128
		for _, h := range hits {
			got = append(got, h.Session)
			if !h.FirstVisit {
				t.Errorf("FirstVisit is false for %v", h)
			}
		}

		first := zint.Uint128{goatcounter.TestSession[0], goatcounter.TestSession[1] + 1}
		want := make([]zint.Uint128, len(wantInt))
		for i := range wantInt {
			want[i] = first
			want[i][1] += uint64(wantInt[i])
		}

		// TODO: test in order.
		sort.Slice(want, func(i, j int) bool { return want[i][1] < want[j][1] })
		var w string
		for _, ww := range want {
			w += ww.Format(16) + " "
		}

		sort.Slice(got, func(i, j int) bool { return got[i][1] < got[j][1] })
		var g string
		for _, gg := range got {
			g += gg.Format(16) + " "
		}

		if w != g {
			t.Errorf("wrong session\nwant: %s\ngot:  %s", w, g)
		}
	}

	var (
		ua1 = `Mozilla/5.0 (X11; Linux x86_64; rv:139.0) Gecko/20100101 Firefox/139.0`
		ua2 = `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/137.0.0.0 Safari/537.36`
	)

	send(ctx1, ua1)
	send(ctx1, ua1)
	send(ctx1, ua2)
	send(ctx2, ua1)
	send(ctx2, ua1)
	send(ctx1, ua1)
	send(ctx1, ua2)

	hits1 := checkHits(ctx1, 5)
	hits2 := checkHits(ctx2, 2)

	want := []int{1, 1, 2, 3, 3, 1, 2}
	checkSess(append(hits1, hits2...), want)

	// Should still use the same sessions.
	goatcounter.SessionTime = 1 * time.Second
	goatcounter.Memstore.EvictSessions(ctx)
	send(ctx1, ua1)
	send(ctx2, ua1)
	hits1 = checkHits(ctx1, 6)
	hits2 = checkHits(ctx2, 3)
	want = []int{1, 1, 2, 3, 3, 1, 2, 1, 3}
	checkSess(append(hits1, hits2...), want)

	// Should use new sessions from now on.
	now := time.Date(2019, 6, 18, 14, 42, 2, 0, time.UTC)
	ctx1 = ztime.WithNow(ctx1, now)
	ctx2 = ztime.WithNow(ctx2, now)
	goatcounter.Memstore.EvictSessions(ctx1)
	send(ctx1, ua1)
	send(ctx2, ua1)
	hits1 = checkHits(ctx1, 7)
	hits2 = checkHits(ctx2, 4)
	want = []int{1, 1, 2, 3, 3, 1, 2, 1, 3, 4, 5}
	checkSess(append(hits1, hits2...), want)
}
