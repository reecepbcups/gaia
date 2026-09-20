// Browser client for the chain's DOOM.
//
// Frames come down a chunked HTTP stream; input goes back up as signed
// transactions, one per tic. Nothing here talks to a game server, because there
// isn't one: the screen is whatever the chain's EndBlocker last rendered, and
// every hash on the page can be read back off the chain.

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

function clock(ms) {
  const d = new Date(ms);
  const p = (n, w = 2) => String(n).padStart(w, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}

const el = (id) => document.getElementById(id);
const screen = el('screen');
const ctx = screen.getContext('2d');
const image = ctx.createImageData(320, 200);
const onlyChanges = el('onlyChanges');
const detail = el('detail');
const detailNote = el('detailnote');

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
      if (entry.hash) row.dataset.hash = entry.hash;
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

const txfeed = el('txfeed');
const pushTx = makeFeed(txfeed);
const pushState = makeFeed(el('statefeed'));

//
// transaction detail
//
// The page signed these bytes, so decoding them here would prove nothing. Ask
// the node to find the transaction by hash and hand back what it stored.
//

function row(label, value) {
  const div = document.createElement('div');
  div.className = 'kv';
  const k = document.createElement('span');
  k.textContent = label;
  const v = document.createElement('span');
  v.textContent = value;
  div.append(k, v);
  return div;
}

function renderTx(hash, res) {
  detail.textContent = '';
  detailNote.textContent = `${res.height ? 'in block ' + res.height : 'pending'}`;

  const ok = Number(res.code || 0) === 0;
  detail.append(
    row('hash', hash),
    row('height', res.height ?? '-'),
    row('result', ok ? 'success' : `failed (code ${res.code})`),
    row('gas', `${res.gas_used ?? '?'} used of ${res.gas_wanted ?? '?'}`),
  );

  if (res.timestamp) detail.append(row('timestamp', res.timestamp));
  if (!ok && res.raw_log) detail.append(row('log', res.raw_log));

  // The interesting part: the decoded messages, straight out of the node.
  const messages = res.tx?.body?.messages ?? [];
  const pre = document.createElement('pre');
  pre.textContent = JSON.stringify(messages.length === 1 ? messages[0] : messages, null, 2);
  detail.appendChild(pre);
}

let selected = null;

async function showTx(hash, rowNode) {
  if (selected) selected.classList.remove('sel');
  selected = rowNode;
  if (selected) selected.classList.add('sel');

  detail.textContent = '';
  detailNote.textContent = 'looking up';
  detail.append(row('hash', hash), row('status', 'querying the node...'));

  // A transaction is only indexed once its block commits, so a miss right after
  // broadcasting is normal. Give it a few blocks.
  for (let attempt = 0; attempt < 12; attempt++) {
    const res = await fetch(`./api/tx/lookup?hash=${hash}`);
    if (res.ok) {
      renderTx(hash, await res.json());
      return;
    }
    if (selected !== rowNode) return;
    await new Promise((r) => setTimeout(r, 250));
  }

  detail.textContent = '';
  detailNote.textContent = 'not found';
  detail.append(row('hash', hash), row('status', 'not indexed, it may have been dropped'));
}

txfeed.addEventListener('click', (e) => {
  const rowNode = e.target.closest('.row');
  if (rowNode?.dataset.hash) showTx(rowNode.dataset.hash, rowNode);
});

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

  el('chain').textContent = session.chainId;
  el('who').textContent = session.address;
  el('seq').textContent = sequence;
  el('tpb').textContent = session.ticsPerBlock;
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
      hash: out.txhash,
      cells: [
        ['num', seq.toString()],
        [buttons === 0 ? 'keys idle' : 'keys', describe(buttons)],
        ['hash', out.txhash ? out.txhash.toLowerCase() : '-'],
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
  const PREFIX = 8 + HASH + 8 + 8;
  const PAYLOAD = PREFIX + PALETTE + PIXELS;

  let frames = 0;
  let windowStart = performance.now();
  let lastHeight = 0n;
  let lastBlockMs = 0;

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
          const head = new DataView(buf.buffer, buf.byteOffset, Math.min(buf.length, 4 + PREFIX));
          const len = head.getUint32(0, true);
          if (len !== PAYLOAD || buf.length < 4 + len) break;

          const tic = head.getBigUint64(4, true);
          const height = head.getBigUint64(44, true);
          const timeMs = Number(head.getBigUint64(52, true));
          const hash = buf.subarray(12, 12 + HASH);
          const palette = buf.subarray(4 + PREFIX, 4 + PREFIX + PALETTE);
          const pixels = buf.subarray(4 + PREFIX + PALETTE, 4 + len);

          draw(palette, pixels);
          el('tic').textContent = tic;
          el('height').textContent = height;

          // One commitment per block, and a block is several frames.
          if (height !== lastHeight) {
            if (lastBlockMs) el('blocktime').textContent = `${timeMs - lastBlockMs}ms`;
            lastBlockMs = timeMs;
            lastHeight = height;
            pushState({
              cells: [
                ['num', height.toString()],
                ['num', `tic ${tic}`],
                ['when', clock(timeMs)],
                ['hash', toHex(hash)],
              ],
            });
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
