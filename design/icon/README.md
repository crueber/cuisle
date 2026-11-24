# Cuisle app icon: "Standup circle"

Chris picked this on 2026-11-24 (design canvas, Round 7, option 5). It shows the team as a ring of seven squares in the
lane colours, with the current speaker in white at the top and a pulse in the middle (*cuisle* = pulse). The body is
the app's ink with a subtle rim, so it still has an edge on dark docks and taskbars.

## Files
| File | Use |
|---|---|
| `cuisle-icon.svg` | Master artwork: 1024×1024 canvas, Apple icon grid (824px body, 100px margin, rx 185) |
| `cuisle-icon-glyph.svg` | The glyph without the body, on transparent. This is the Icon Composer layer. |
| `appicon.png` | 1024×1024 RGBA, a drop-in for `build/appicon.png` |
| `icons.icns` | macOS, 16 to 1024 @1x/@2x. Drop-in for `build/darwin/icons.icns` |
| `icon.ico` | Windows, 16/32/48/64/128/256. Drop-in for `build/windows/icon.ico` |
| `appicon.icon/` | Icon Composer bundle (ink fill + the glyph layer). Replaces `build/appicon.icon/` |
| `png/cuisle-{16…1024}.png` | Each size rendered from the vector, for the favicon, project page, README and About |

Colours are the app tokens: ink `#16181D`, rim `#3A3E46`, paper `#F6F4EE`, and the lanes (To Do `#8C8676`, In Progress
`#3D6FC4`, In Review `#7A55B8`, QA `#C08A2A`, Done `#5E8C6A`).

## Install (for Claude Code)
1. Copy the files in:
   - `appicon.png` → `build/appicon.png`
   - `icons.icns` → `build/darwin/icons.icns`
   - `icon.ico` → `build/windows/icon.ico`
   - Replace `build/appicon.icon/` with `appicon.icon/`, and delete the old `Assets/wails_icon_vector.svg`.
   - Linux (AppImage, `.desktop`, nfpm) already reads `build/appicon.png`, so nothing else is needed there.
2. **macOS `Assets.car`:**
   - `build/darwin/Assets.car` still holds the Wails icon, and macOS prefers it when present.
   - On a Mac, run `task common:generate:icons`; it runs `wails3 generate icons … -iconcomposerinput appicon.icon -macassetdir darwin`.
     That regenerates `Assets.car`, `icons.icns` and `icon.ico` from these sources.
   - If the Icon Composer step fails, check `appicon.icon/icon.json`. The `"fill": {"solid": …}` key follows Icon
     Composer's format as I understand it but wasn't verified on a Mac. Fix it, or fall back to deleting `Assets.car` so
     `icons.icns` is used.
3. **DMG:** if `build/darwin/dmg-file-icon.{png,icns}` are still the Wails defaults, regenerate them from `appicon.png`
   (the PNG is 1254px; scale up `cuisle-icon.svg`).
4. **Web surfaces:** set the GitHub Pages project page and README favicon/logo to `png/cuisle-32.png` and `cuisle-icon.svg`.
5. **Verify:**
   - `task package` on macOS; the icon shows in Finder, the Dock and the DMG.
   - The Linux AppImage/`.deb` icon.
   - The Windows installer and taskbar from the GitHub build.
   - Commit as `build: Cuisle app icon (standup circle)`.

## Installed (2026-11-24)
- `build/appicon.png`, `build/appicon.icon/` (Wails vector removed), `build/darwin/icons.icns`, `build/windows/icon.ico`.
  `task package` regenerates the `.icns`/`.ico` from `appicon.png` (`common:generate:icons`), so those two follow the PNG.
- `build/darwin/Assets.car` deleted: this Mac has only the Command Line Tools (no `actool`), so it can't be rebuilt
  here, and the old one held the Wails icon. macOS falls back to `CFBundleIconFile` = `icons.icns`. A Mac with Xcode
  (or the GitHub macOS runner) can regenerate it with `task common:generate:icons` for the macOS 26 layered look.
- DMG: `dmg-file-icon.{png,icns}` and `dmg-background.png` rendered by `scripts/icon/render.mjs`.
- Linux: nfpm installs `png/cuisle-{32…512}.png` into the matching hicolor sizes.
- README and the project page (`site/`) use `png/cuisle-128.png` and `cuisle-icon.svg` (favicon, header, hero, footer).
