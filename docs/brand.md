# Keyosk brand

Keyosk is written in lowercase, **keyosk**, when it is set as the wordmark, and
**Keyosk** in running text.

## Files

| File | Use |
| --- | --- |
| `web/static/brand/keyosk-logo-{light,dark}.svg` | Mark and wordmark, transparent background |
| `web/static/brand/keyosk-mark-{light,dark}.svg` | Mark alone |
| `web/static/favicon.svg`, `favicon.ico`, `apple-touch-icon.png` | Browser and home-screen icons. The SVG follows the OS colour scheme |
| `docs/brand/readme-banner-{light,dark}.svg` | Banner at the top of the README |
| `website/` | The website and documentation (Zensical). Its theme is `website/docs/stylesheets/extra.css`; the logo, favicon and fonts are copied in from `web/static` by `make website-assets`, so change them there |
| `docs/brand/social-preview.{svg,png}` | GitHub social preview (1280×640). Upload the PNG under *Settings → Social preview*; it is not picked up from the repository |

The wordmark in the SVG files is converted to outlines, so they need no font.
The header in the app draws the mark inline (`brand-mark` in
`web/templates/layout.html`) so it can follow the page's colours, and sets the
name as text.

The mark is an awning, a booth and a counter with a key cut out of the booth.
At 20 px and below the stripes are dropped and the key gets thicker strokes
(see `favicon.svg`).

## Colours

| | Light | Dark |
| --- | --- | --- |
| Background | `#F6F3EC` | `#121817` |
| Cards (`--surface`) | `#FFFFFF` | `#1A2120` |
| Line | `#E4DFD4` | `#2C3836` |
| Ink | `#1F2A2A` | `#F2EEE6` |
| Muted text | `#4A5654` | `#A9B3B0` |
| Accent | `#1E7A6E` | `#52B5A6` |
| Text on accent | `#FFFFFF` | `#0E1413` |

The UI tokens in the `:root` block of `web/static/style.css` carry these
values (`--bg`, `--text`, `--primary`, …). Use the tokens, not the hex values.

## Fonts

- **Space Grotesk** (variable, 300–700) for text and the wordmark (700).
- **IBM Plex Mono** (400, 500) for everything copied verbatim, and for small
  labels.

Both are licensed under the SIL Open Font License 1.1; the licence texts are in
`web/static/fonts/`. They are served from `/static/fonts/` (latin and latin-ext
subsets), because the content security policy allows no external font sources.
