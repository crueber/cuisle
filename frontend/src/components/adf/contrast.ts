// textColor clamp (ux-spec §14.4, architecture §4.9): keep the authored hue, move lightness until the text meets
// 4.5:1 against the theme's page background. Pure functions, no DOM. The backgrounds mirror design/tokens.json
// color.light.bg and color.dark.bg (CSS tokens cannot be read from here).

export type Theme = "light" | "dark";
type RGB = [number, number, number];

const MIN_CONTRAST = 4.5;
const PAGE_BG: Record<Theme, RGB> = {
  light: [0xf6, 0xf4, 0xee],
  dark: [0x12, 0x14, 0x18],
};

const cache = new Map<string, string | null>();

/** Parses an authored "#rrggbb" (case-insensitive). Anything else is null: the mark is then dropped. */
function parseHex(raw: string): RGB | null {
  if (raw.length !== 7 || raw.charAt(0) !== "#") return null;
  const digits = raw.slice(1);
  if (!/^[0-9a-f]{6}$/i.test(digits)) return null;
  const n = Number.parseInt(digits, 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function lin(c: number): number {
  const v = c / 255;
  return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
}

function luminance([r, g, b]: RGB): number {
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

function contrast(a: RGB, b: RGB): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

/** HSL with hue in degrees; the same hue as the RGB hue the tests measure. */
function toHsl([r, g, b]: RGB): [number, number, number] {
  const R = r / 255;
  const G = g / 255;
  const B = b / 255;
  const max = Math.max(R, G, B);
  const min = Math.min(R, G, B);
  const l = (max + min) / 2;
  const d = max - min;
  if (d === 0) return [0, 0, l];
  const s = d / (1 - Math.abs(2 * l - 1));
  let h: number;
  if (max === R) h = ((G - B) / d) % 6;
  else if (max === G) h = (B - R) / d + 2;
  else h = (R - G) / d + 4;
  return [(h * 60 + 360) % 360, s, l];
}

function fromHsl(h: number, s: number, l: number): RGB {
  const c = (1 - Math.abs(2 * l - 1)) * s;
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
  const m = l - c / 2;
  let rgb: RGB;
  if (h < 60) rgb = [c, x, 0];
  else if (h < 120) rgb = [x, c, 0];
  else if (h < 180) rgb = [0, c, x];
  else if (h < 240) rgb = [0, x, c];
  else if (h < 300) rgb = [x, 0, c];
  else rgb = [c, 0, x];
  return [rgb[0], rgb[1], rgb[2]].map((v) => Math.round((v + m) * 255)) as RGB;
}

function css([r, g, b]: RGB): string {
  return `rgb(${r}, ${g}, ${b})`;
}

function clamp(authored: string, theme: Theme): string | null {
  const rgb = parseHex(authored);
  if (!rgb) return null;
  const bg = PAGE_BG[theme];
  if (contrast(rgb, bg) >= MIN_CONTRAST) return css(rgb);
  const [h, s, l0] = toHsl(rgb);
  // On a light page darken, on a dark page lighten; stop at the first step that passes (smallest change).
  const step = luminance(bg) > 0.5 ? -0.01 : 0.01;
  let out = rgb;
  let l = l0;
  for (let i = 0; i < 100; i++) {
    l = Math.min(1, Math.max(0, l + step));
    out = fromHsl(h, s, l);
    if (contrast(out, bg) >= MIN_CONTRAST) break;
  }
  return css(out);
}

/**
 * The CSS colour to paint authored text with on this theme: the authored colour when it already meets 4.5:1, otherwise
 * the same hue at the nearest lightness that does. Null when the authored value is not "#rrggbb".
 */
export function clampTextColor(authored: string, theme: Theme): string | null {
  const key = `${theme}|${authored}`;
  const hit = cache.get(key);
  if (hit !== undefined) return hit;
  const out = clamp(authored, theme);
  cache.set(key, out);
  return out;
}
