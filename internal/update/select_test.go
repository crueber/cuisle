package update

import "testing"

func TestParseVersionAndCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.10.0", "v1.9.9", 1},
		{"v2.0.0", "v1.99.99", 1},
		{"1.2.3", "v1.2.3", 0},
		{"v1.2.3-rc.1", "v1.2.3", -1},
		{"v1.2.3+build.5", "v1.2.3", 0},
	}
	for _, c := range cases {
		a, ok1 := ParseVersion(c.a)
		b, ok2 := ParseVersion(c.b)
		if !ok1 || !ok2 {
			t.Fatalf("parse %q %q", c.a, c.b)
		}
		if got := a.Compare(b); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, bad := range []string{"", "dev", "v1.2", "v1.2.3.4", "v1.x.3", "v01.2.3", "v1.2.3-"} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestLatest_SkipsDraftsPrereleasesAndNonVersions(t *testing.T) {
	rs := []Release{
		{Tag: "v1.2.0"}, {Tag: "v1.5.0", Draft: true}, {Tag: "v1.4.0", Prerelease: true}, {Tag: "v1.4.1-rc.1"},
		{Tag: "nightly"}, {Tag: "v1.3.0"},
	}
	got, ok := Latest(rs)
	if !ok || got.Tag != "v1.3.0" {
		t.Fatalf("got %+v %v", got, ok)
	}
	if _, ok := Latest([]Release{{Tag: "v9.0.0", Draft: true}}); ok {
		t.Fatal("a draft was offered")
	}
}

// Names as the release workflows write them: the dmg and setup.exe names are set in scripts/publish/release.yml; the
// Linux AppImage and .deb names come from wails3/nfpm (bin/*.AppImage, bin/*.deb) and were not observed on a real
// release, so they are matched by pattern and these are representative.
var releaseAssets = []Asset{
	{Name: "Cuisle-v1.3.0-macos-universal.dmg"},
	{Name: "Cuisle-v1.3.0-windows-amd64-setup.exe"},
	{Name: "cuisle-x86_64.AppImage"},
	{Name: "cuisle_0.0.1_amd64.deb"},
	{Name: "SHA256SUMS.txt"},
	{Name: "SHA256SUMS.txt.sig"},
}

func TestSelectAsset_Table(t *testing.T) {
	armAssets := append([]Asset{
		{Name: "cuisle-aarch64.AppImage"}, {Name: "Cuisle-v1.3.0-windows-arm64-setup.exe"},
	}, releaseAssets...)
	cases := []struct {
		name         string
		assets       []Asset
		goos, goarch string
		appImage     bool
		want         string
		kind         Kind
	}{
		{"mac arm64 takes universal", releaseAssets, "darwin", "arm64", false, "Cuisle-v1.3.0-macos-universal.dmg", KindDMG},
		{"mac amd64 takes universal", releaseAssets, "darwin", "amd64", false, "Cuisle-v1.3.0-macos-universal.dmg", KindDMG},
		{"windows amd64", releaseAssets, "windows", "amd64", false, "Cuisle-v1.3.0-windows-amd64-setup.exe", KindNSIS},
		{"windows arm64 without an arm build", releaseAssets, "windows", "arm64", false, "", ""},
		{"windows arm64 with one", armAssets, "windows", "arm64", false, "Cuisle-v1.3.0-windows-arm64-setup.exe", KindNSIS},
		{"linux AppImage amd64", releaseAssets, "linux", "amd64", true, "cuisle-x86_64.AppImage", KindAppImage},
		{"linux AppImage arm64 picks aarch64", armAssets, "linux", "arm64", true, "cuisle-aarch64.AppImage", KindAppImage},
		{"linux AppImage arm64 without one", releaseAssets, "linux", "arm64", true, "", ""},
		{"linux deb install opens the page", releaseAssets, "linux", "amd64", false, "cuisle_0.0.1_amd64.deb", KindPage},
		{"bare AppImage name is amd64", []Asset{{Name: "cuisle.AppImage"}}, "linux", "amd64", true, "cuisle.AppImage", KindAppImage},
		{"bare AppImage name is not arm64", []Asset{{Name: "cuisle.AppImage"}}, "linux", "arm64", true, "", ""},
		{"no mac asset", []Asset{{Name: "cuisle.AppImage"}}, "darwin", "arm64", false, "", ""},
		{"freebsd", releaseAssets, "freebsd", "amd64", false, "", ""},
		{"never the signature", []Asset{{Name: "x.dmg.sig"}}, "darwin", "arm64", false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, kind, ok := SelectAsset(c.assets, c.goos, c.goarch, c.appImage)
			if c.want == "" {
				if ok {
					t.Fatalf("picked %s", a.Name)
				}
				return
			}
			if !ok || a.Name != c.want || kind != c.kind {
				t.Fatalf("got %q %q %v, want %q %q", a.Name, kind, ok, c.want, c.kind)
			}
		})
	}
}
