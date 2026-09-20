// Browser client for the chain's DOOM.
//
// Frames come down a chunked HTTP stream; input goes back up as signed
// transactions, one per tic. Nothing here talks to a game server, because there
// isn't one: the screen is whatever the chain's EndBlocker last rendered.

import { signInput, getPublicKey, hexToBytes, concat } from './tx.js';

// Matches the Button enum in proto/gaia/doom/v1/doom.proto.
const BUTTONS = {
  ArrowUp: 0, KeyW: 0,
  ArrowDown: 1, KeyS: 1,
  ArrowLeft: 2,
  ArrowRight: 3,
  KeyA: 4,
  KeyD: 5,
  ControlLeft: 6, ControlRight: 6, KeyF: 6,
  Space: 7, KeyE: 7,
  ShiftLeft: 8, ShiftRight: 8,
  AltLeft: 9, AltRight: 9,
  Escape: 10,
  Enter: 11,
  Tab: 12,
  KeyY: 13,
  KeyN: 14,
  Digit1: 15, Digit2: 16, Digit3: 17, Digit4: 18,
  Digit5: 19, Digit6: 20, Digit7: 21,
};

// Same order as the enum, for turning a mask back into something readable.
const NAMES = [
  'fwd', 'back', 'left', 'right', 'strafeL', 'strafeR', 'fire', 'use',
  'run', 'strafe', 'esc', 'enter', 'map', 'y', 'n',
  'wpn1', 'wpn2', 'wpn3', 'wpn4', 'wpn5', 'wpn6', 'wpn7',
];

function describe(mask) {
  if (mask === 0) return 'idle';
  const out = [];
  for (let i = 0; i < NAMES.length; i++) {
    if (mask & (1 << i)) out.push(NAMES[i]);
  }
  return out.join(' ');
}

function toHex(bytes, chars) {
  let s = '';
  for (const b of bytes) s += b.toString(16).padStart(2, '0');
  return chars ? s.slice(0, chars) : s;
}

const el = (id) => document.getElementById(id);
const screen = el('screen');
const ctx = screen.getContext('2d');
const image = ctx.createImageData(320, 200);
const onlyChanges = el('onlyChanges');

let session = null;
let privKey = null;
let pubKey = null;
let sequence = 0n;
let txCount = 0;
let badCount = 0;
let held = 0;
let playing = false;

function fail(err) {
  el('err').textContent = String(err && err.message ? err.message : err);
}

//
// feeds
//
// A held key is one transaction per block, so these fill at tens of rows a
// second. Rows are batched into an animation frame and the lists are capped,
// otherwise the DOM ends up doing more work than the game does.
//

const MAX_ROWS = 150;

function makeFeed(node) {
  let pending = [];
  let scheduled = false;

  const flush = () => {
    scheduled = false;

    const frag = document.createDocumentFragment();
    for (const entry of pending) {
      const row = document.createElement('div');
      row.className = entry.bad ? 'row bad' : 'row';
      for (const [className, text] of entry.cells) {
        const cell = document.createElement('span');
        cell.className = className;
        cell.textContent = text;
        row.appendChild(cell);
      }
      frag.appendChild(row);
    }
    pending = [];

    node.insertBefore(frag, node.firstChild);
    while (node.childElementCount > MAX_ROWS) node.removeChild(node.lastElementChild);
  };

  return (entry) => {
    // Newest first, so a batch goes in reversed.
    pending.unshift(entry);
    if (!scheduled) {
      scheduled = true;
      requestAnimationFrame(flush);
    }
  };
}

const pushTx = makeFeed(el('txfeed'));
const pushState = makeFeed(el('statefeed'));

//
// session
//

async function connect() {
  const res = await fetch('./api/session');
  if (!res.ok) throw new Error((await res.json()).error || res.statusText);

  session = await res.json();
  privKey = hexToBytes(session.privateKey);
  pubKey = getPublicKey(privKey, true);
  sequence = BigInt(session.sequence);

  el('who').textContent = `${session.address} on ${session.chainId}`;
  el('seq').textContent = sequence;
}

async function resyncSequence() {
  const res = await fetch(`./api/account?address=${session.address}`);
  if (!res.ok) return;
  const acct = await res.json();
  sequence = BigInt(acct.sequence);
  el('seq').textContent = sequence;
}

//
// input
//
// One transaction per tic, held buttons and all. The chain clears its pending
// input every block, so releasing a key is just the next transaction not
// setting that bit.
//

let lastLogged = null;
let tpsWindow = 0;
let tpsStart = performance.now();

async function sendInput(buttons) {
  const seq = sequence;
  const tx = await signInput({ session, privKey, pubKey, sequence: seq, buttons });

  const res = await fetch('./api/tx', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tx }),
  });

  const out = await res.json();
  if (out.error) throw new Error(out.error);

  const rejected = Boolean(out.code && out.code !== 0);

  // Every tic is a transaction, which is the point but also unreadable. Default
  // to logging the ones that changed something, plus anything that failed.
  if (rejected || buttons !== lastLogged || !onlyChanges.checked) {
    pushTx({
      bad: rejected,
      cells: [
        ['num', seq.toString()],
        [buttons === 0 ? 'keys idle' : 'keys', describe(buttons)],
        ['hash', out.txhash ? out.txhash.slice(0, 16).toLowerCase() : '-'],
      ],
    });
  }
  lastLogged = buttons;

  if (rejected) {
    // Almost always a sequence that drifted because a transaction was dropped.
    badCount++;
    el('bad').textContent = badCount;
    await resyncSequence();
    throw new Error(out.rawLog || `tx code ${out.code}`);
  }

  sequence += 1n;
  txCount++;
  el('seq').textContent = sequence;
  el('txs').textContent = txCount;

  tpsWindow++;
  const now = performance.now();
  if (now - tpsStart >= 1000) {
    el('tps').textContent = Math.round((tpsWindow * 1000) / (now - tpsStart));
    tpsWindow = 0;
    tpsStart = now;
  }
}

// Sending is serialised: broadcasts have to reach the mempool in sequence
// order, and a local round trip is a couple of milliseconds against a 28ms tic.
async function inputLoop() {
  for (;;) {
    const started = performance.now();

    if (playing && session) {
      try {
        await sendInput(held);
        el('err').textContent = '';
      } catch (err) {
        fail(err);
      }
    }

    const elapsed = performance.now() - started;
    await new Promise((r) => setTimeout(r, Math.max(0, 1000 / 35 - elapsed)));
  }
}

function bindKeys() {
  const set = (code, down) => {
    const bit = BUTTONS[code];
    if (bit === undefined) return false;
    held = down ? (held | (1 << bit)) : (held & ~(1 << bit));
    held >>>= 0;
    return true;
  };

  screen.addEventListener('keydown', (e) => {
    if (set(e.code, true)) e.preventDefault();
  });
  screen.addEventListener('keyup', (e) => {
    if (set(e.code, false)) e.preventDefault();
  });

  screen.addEventListener('focus', () => { playing = true; });
  screen.addEventListener('blur', () => { playing = false; held = 0; });
}

//
// frames
//

function draw(palette, pixels) {
  const data = image.data;
  for (let i = 0, p = 0; i < pixels.length; i++, p += 4) {
    const c = pixels[i] * 4;
    // Palette entries are BGRA, matching DOOM's in-memory colour struct.
    data[p] = palette[c + 2];
    data[p + 1] = palette[c + 1];
    data[p + 2] = palette[c];
    data[p + 3] = 255;
  }
  ctx.putImageData(image, 0, 0);
}

async function frameLoop() {
  const HASH = 32;
  const PALETTE = 1024;
  const PIXELS = 320 * 200;
  const PAYLOAD = 8 + HASH + PALETTE + PIXELS;

  let frames = 0;
  let windowStart = performance.now();
  let lastHash = '';

  for (;;) {
    try {
      const res = await fetch('./api/frames');
      const reader = res.body.getReader();

      let buf = new Uint8Array(0);

      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;

        buf = concat([buf, value]);

        while (buf.length >= 4) {
          const len = new DataView(buf.buffer, buf.byteOffset, 4).getUint32(0, true);
          if (len !== PAYLOAD || buf.length < 4 + len) break;

          const tic = new DataView(buf.buffer, buf.byteOffset + 4, 8).getBigUint64(0, true);
          const hash = buf.subarray(12, 12 + HASH);
          const palette = buf.subarray(12 + HASH, 12 + HASH + PALETTE);
          const pixels = buf.subarray(12 + HASH + PALETTE, 4 + len);

          draw(palette, pixels);
          el('tic').textContent = tic;

          // One commitment per block, and a block is several frames.
          const hex = toHex(hash, 40);
          if (hex !== lastHash) {
            lastHash = hex;
            pushState({ cells: [['num', tic.toString()], ['hash', hex]] });
          }

          frames++;
          const now = performance.now();
          if (now - windowStart >= 1000) {
            el('fps').textContent = Math.round((frames * 1000) / (now - windowStart));
            frames = 0;
            windowStart = now;
          }

          buf = buf.slice(4 + len);
        }
      }
    } catch (err) {
      fail(err);
    }

    // The stream drops whenever the node restarts. Just pick it back up.
    await new Promise((r) => setTimeout(r, 500));
  }
}

connect().then(bindKeys).catch(fail);
frameLoop();
inputLoop();
