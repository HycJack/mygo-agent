package main

// The login screen: a brand panel and one card with the two ways in —
// the password form, which issues the session's JWT, and the three
// OAuth providers (WeChat, Google, GitHub). The flows are mocked
// in-process: a beat of handshake, then a demo identity — no network
// leaves the window, the way the rest of the example's data is mock.

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

func (d *dashboard) loginView(c *ui.Context, pal Palette) {
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		// The brand side: a gradient the charts' hues already speak, the
		// product, and what it is.
		ui.Column(c).Grow(1).MinWidth(0).Padding(44).Gap(24).
			Gradient(pal.Series[0], pal.Series[3], 135).Children(func() {
			white := ui.RGB(255, 255, 255)
			soft := white.Alpha(0.72)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(34, 34).Radius(10).Background(white.Alpha(0.18)).Center().Children(func() {
					ui.Icon(c, IconZap).FontSize(19).TextColor(white)
				})
				ui.Text(c, "Acme Analytics").FontSize(16).FontWeight(700).TextColor(white)
			})
			ui.Box(c).Grow(1)
			ui.Column(c).Gap(12).MaxWidth(400).Children(func() {
				ui.Text(c, "Every pixel drawn from Go.").FontSize(30).FontWeight(700).TextColor(white)
				ui.Text(c, "No WebView, no HTML, no JavaScript — the whole dashboard, login included, is GPU-drawn by MyGo.").
					FontSize(14).TextColor(soft)
			})
			ui.Box(c).Grow(1)
			ui.Column(c).Gap(10).Children(func() {
				for _, f := range []struct {
					icon *ui.SVG
					text string
				}{
					{IconActivity, "Live metrics pushed from goroutines"},
					{IconLayers, "Lists, tables and charts over mock data"},
					{IconMoon, "Light and dark, swapped at runtime"},
				} {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(26, 26).Radius(8).Background(white.Alpha(0.16)).Center().Children(func() {
							ui.Icon(c, f.icon).FontSize(14).TextColor(white)
						})
						ui.Text(c, f.text).FontSize(13).TextColor(soft)
					})
				}
			})
			ui.Text(c, "© 2026 Acme, Inc. — a MyGo example, nothing here is real.").FontSize(11.5).TextColor(white.Alpha(0.55))
		})

		// The form side: the sign-in card, centred, with the theme
		// toggle in its corner.
		ui.Column(c).Width(460).Shrink(0).FillHeight().Background(pal.Bg).Padding(28).Children(func() {
			ui.Row(c).Children(func() {
				ui.Spacer(c)
				iconToggle(c, pal, "Toggle theme", d.dark, IconMoon, func() { d.dark = !d.dark })
			})
			ui.Box(c).Grow(1)
			ui.Column(c).Width(320).Margin(0, ui.Auto).Gap(18).Children(func() {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, "Welcome back").FontSize(22).FontWeight(700)
					ui.Text(c, "Sign in to Acme Analytics.").FontSize(13).TextColor(pal.TextMuted)
				})

				// The JWT way: credentials in, a token out. In production
				// this round-trips the auth server; here it mints a demo
				// token the footer menu can copy.
				ui.Form(c, func() {
					ui.Field(c, "Email", func() {
						if ui.TextInput(c, &d.loginEmail).Placeholder("ada@acme.dev").Submitted() {
							d.signInWithPassword()
						}
					})
					ui.Field(c, "Password", func() {
						if ui.TextInput(c, &d.loginPassword).Password().Placeholder("••••••••").Submitted() {
							d.signInWithPassword()
						}
					}).Description("Any email and a password of 6+ characters; the session is a JWT.")
				})
				if d.authError != "" {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						ui.Icon(c, IconAlert).FontSize(13).TextColor(pal.Danger)
						ui.Text(c, d.authError).FontSize(12).TextColor(pal.Danger)
					})
				}
				signIn := ui.PrimaryButton(c, "")
				if d.authBusy == "password" {
					signIn = signIn.Children(func() {
						ui.Spinner(c)
						ui.Text(c, "Signing in…")
					})
				} else {
					signIn = signIn.Children(func() { ui.Text(c, "Sign in").SingleLine() })
				}
				signIn.FillWidth().Disabled(d.authBusy != "")
				if signIn.Clicked() {
					d.signInWithPassword()
				}

				ui.Row(c).Gap(10).AlignItems(ui.Center).PaddingY(2).Children(func() {
					ui.Box(c).Grow(1).Height(1).Background(pal.Border)
					ui.Text(c, "or continue with").FontSize(11.5).TextColor(pal.TextMuted)
					ui.Box(c).Grow(1).Height(1).Background(pal.Border)
				})

				// The OAuth way, one provider per button. While a handshake
				// "runs", the button spins and the rest stay disabled.
				for _, p := range []struct {
					name string
					icon *ui.SVG
				}{
					{"WeChat", IconWeChat},
					{"Google", IconGoogle},
					{"GitHub", IconGitHub},
				} {
					p := p
					btn := ui.Button(c, "").FillWidth()
					if d.authBusy == p.name {
						btn.Children(func() {
							ui.Spinner(c)
							ui.Textf(c, "Connecting to %s…", p.name).SingleLine()
						})
					} else {
						btn.Children(func() {
							ui.Icon(c, p.icon).FontSize(16)
							ui.Textf(c, "Continue with %s", p.name).SingleLine()
						})
					}
					btn.Disabled(d.authBusy != "")
					if btn.Clicked() {
						d.signInWith(p.name)
					}
				}

				ui.Text(c, "OAuth and JWT are mocked in-process; nothing leaves this window.").
					FontSize(11).TextColor(pal.TextMuted).TextAlign(ui.Center)
			})
			ui.Box(c).Grow(1)
		})
	})
}

// signInWithPassword checks the demo credentials and, when they pass,
// issues the session's JWT. In production this round-trips the auth
// server; here the "server" is a goroutine's beat of latency.
func (d *dashboard) signInWithPassword() {
	if d.authBusy != "" {
		return
	}
	email := strings.TrimSpace(d.loginEmail)
	switch {
	case !strings.Contains(email, "@"):
		d.authError = "Enter an email address, such as ada@acme.dev."
		return
	case len(d.loginPassword) < 6:
		d.authError = "The password needs at least 6 characters."
		return
	}
	d.authError = ""
	d.handshake("password", 700*time.Millisecond)
}

// signInWith runs the mock OAuth handshake for a provider.
func (d *dashboard) signInWith(provider string) {
	if d.authBusy != "" {
		return
	}
	d.handshake(provider, 900*time.Millisecond)
}

// handshake busy-ies the form for a beat, then lands the session on the
// UI thread through Window.Update. Headless (the tests) has no window,
// so it lands at once.
func (d *dashboard) handshake(who string, after time.Duration) {
	if d.win == nil {
		d.completeSignIn(who)
		return
	}
	d.authBusy = who
	win := d.win
	go func() {
		time.Sleep(after)
		win.Update(func() { d.completeSignIn(who) })
	}()
}

// completeSignIn lands the session: the identity, the JWT, and the
// toast the shell shows on its first frame.
func (d *dashboard) completeSignIn(who string) {
	d.authBusy = ""
	d.loggedIn = true
	if strings.Contains(who, "@") {
		d.user = who
		d.welcome = "Signed in — a JWT was issued for " + who
	} else {
		d.user = "ada@acme.dev"
		d.welcome = "Signed in with " + who
	}
	d.jwt = fakeJWT(d.user)
}

// fakeJWT mints a demo token with the real shape — header.payload.
// signature, base64url JSON — and no secret behind the signature. The
// footer menu's "Copy JWT" hands it out.
func fakeJWT(sub string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(
		`{"sub":%q,"name":"Ada Lovelace","iss":"acme-analytics","exp":%d}`,
		sub, time.Now().Add(24*time.Hour).Unix())))
	return header + "." + payload + ".d3m0-s1gn4tur3"
}
