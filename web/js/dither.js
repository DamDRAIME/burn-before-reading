// Dithering, like a thermal print head rendering a gradient with dots.
// Builds small mask images once and exposes them as CSS custom properties that
// styles.css uses as masks. Each is a tile, opaque at the top and thinning out
// into dots towards the bottom:
//   --dither-ramp-lg / -sm     ordered (Bayer), dissolves completely (circle, emblems)
//   --dither-ramp-text         ordered, only thins out (History title)
//   --dither-ramp-title / -light  random scatter, only thins out (Write title)
// Without canvas the properties stay unset and the masks are simply skipped.

const BAYER_8 = [
  [0, 32, 8, 40, 2, 34, 10, 42],
  [48, 16, 56, 24, 50, 18, 58, 26],
  [12, 44, 4, 36, 14, 46, 6, 38],
  [60, 28, 52, 20, 62, 30, 54, 22],
  [3, 35, 11, 43, 1, 33, 9, 41],
  [51, 19, 59, 27, 49, 17, 57, 25],
  [15, 47, 7, 39, 13, 45, 5, 37],
  [63, 31, 55, 23, 61, 29, 53, 21],
];

const DOT = 2; // CSS px per printed dot
const BAYER_TILE = 8 * DOT; // one matrix period: repeats horizontally seamlessly
const NOISE_TILE = 128; // wide enough that the random pattern doesn't visibly repeat

/** Small seeded PRNG (mulberry32), so the scatter is the same on every visit. */
function random(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * A mask tile: full coverage at the top, fading over `fade` px down to `floor`
 * (0 = gone, 0.9 = still 90% of the dots), then holding `floor` for `tail` more px.
 * `threshold(row, col)` decides each dot: a Bayer cell for an ordered pattern,
 * a random number for a scattered one. Sizes are in CSS px, multiples of DOT.
 */
function ramp({ fade, floor = 0, tail = 0, width, threshold }, scale) {
  const canvas = document.createElement('canvas');
  canvas.width = width * scale;
  canvas.height = (fade + tail) * scale;
  const g = canvas.getContext('2d');
  if (!g) return null;
  g.fillStyle = '#000';
  const fadeRows = fade / DOT;
  const rows = (fade + tail) / DOT;
  const cols = width / DOT;
  for (let row = 0; row < rows; row++) {
    // Smoothstep: dots go missing gently at the top of the ramp instead of all at once.
    const t = Math.min(1, (row + 0.5) / fadeRows);
    const coverage = 1 - (1 - floor) * t * t * (3 - 2 * t);
    for (let col = 0; col < cols; col++) {
      if (coverage > threshold(row, col)) {
        g.fillRect(col * DOT * scale, row * DOT * scale, DOT * scale, DOT * scale);
      }
    }
  }
  return canvas.toDataURL('image/png');
}

const bayer = (row, col) => (BAYER_8[row % 8][col % 8] + 0.5) / 64;

/** A fresh random threshold per dot, from a fixed seed. */
const scatter = (seed) => random(seed);

export function installDither() {
  try {
    // Draw at device resolution so the dots stay crisp instead of being smoothed when scaled.
    const scale = Math.max(1, Math.ceil(window.devicePixelRatio || 1));
    const set = (name, url) => {
      if (url) document.documentElement.style.setProperty(name, `url("${url}")`);
    };

    set('--dither-ramp-lg', ramp({ fade: 160, width: BAYER_TILE, threshold: bayer }, scale));
    set('--dither-ramp-sm', ramp({ fade: 32, width: BAYER_TILE, threshold: bayer }, scale));

    // History title: the fade ends at the baseline; the flat tail covers descenders (y).
    set('--dither-ramp-text', ramp({ fade: 20, floor: 0.86, tail: 26, width: BAYER_TILE, threshold: bayer }, scale));

    // Write title: the same idea with randomly scattered dots over a taller band,
    // so missing dots don't line up in rows. "Burn" and "Before" are sparser.
    set('--dither-ramp-title', ramp({ fade: 40, floor: 0.9, tail: 26, width: NOISE_TILE, threshold: scatter(7) }, scale));
    set('--dither-ramp-title-light', ramp({ fade: 40, floor: 0.97, tail: 26, width: NOISE_TILE, threshold: scatter(11) }, scale));
  } catch {
    // No canvas: shapes stay solid.
  }
}
