// The explorer. Everything it draws arrives as a server sent event carrying a
// block the node already committed, so a row appearing here means the
// transaction is in a block, not that the gateway thinks it sent one.

const el = (id) => document.getElementById(id);

const MAX_ROWS = 300;

// Block times, newest last, for the rate readout.
const times = [];

let selected = null;

function short(hash) {
  return hash.slice(0, 10);
}

function clock(ms) {
  const d = new Date(ms);
  return d.toTimeString().slice(0, 8) + '.' + String(d.getMilliseconds()).padStart(3, '0');
}

function trim(feed) {
  while (feed.childElementCount > MAX_ROWS) feed.removeChild(feed.lastChild);
}

function hashCell(hash) {
  const span = document.createElement('span');
  span.className = 'hash';
  span.textContent = short(hash);
  span.title = hash;
  span.onclick = () => showTx(hash, span);
  return span;
}

function summarise(tx) {
  const s = tx.summary;
  const parts = [];
  if (s.breaks) parts.push(s.breaks + ' break');
  if (s.places) parts.push(s.places + ' place');
  if (s.chats) parts.push(s.chats + ' chat');
  if (s.hotbar) parts.push(s.hotbar + ' hotbar');
  if (s.moves) parts.push(s.moves + ' move');
  return parts.join(', ') || 'nothing';
}

function addBlock(b) {
  times.push(b.time);
  if (times.length > 60) times.shift();
  if (times.length > 1) {
    const span = (times[times.length - 1] - times[0]) / 1000;
    el('rate').textContent = span > 0 ? ((times.length - 1) / span).toFixed(2) : '-';
  }

  el('height').textContent = b.height;

  const feed = el('blocks');
  const row = document.createElement('div');
  row.className = 'row' + (b.txs.length ? '' : ' empty');

  const height = document.createElement('span');
  height.textContent = '#' + b.height;

  const time = document.createElement('span');
  time.textContent = clock(b.time);

  const count = document.createElement('span');
  count.textContent = b.txs.length ? b.txs.length + ' tx' : '-';

  const last = document.createElement('span');
  if (b.txs.length) {
    last.appendChild(hashCell(b.txs[0].hash));
  } else {
    last.textContent = 'empty';
  }

  row.append(height, time, count, last);
  feed.insertBefore(row, feed.firstChild);
  trim(feed);

  for (const tx of b.txs) addEdits(tx);
}

// Only the edits go in the second feed. Twenty position reports a second would
// bury them, and they are in the transaction detail anyway.
function addEdits(tx) {
  const feed = el('edits');

  for (const a of tx.actions) {
    if (a.kind !== 'break' && a.kind !== 'place') continue;

    const row = document.createElement('div');
    row.className = 'row' + (tx.known && tx.code !== 0 ? ' bad' : '');

    const kind = document.createElement('span');
    kind.className = 'tag ' + a.kind;
    kind.textContent = a.kind;

    const what = document.createElement('span');
    what.textContent = a.block ? a.block.replace('minecraft:', '') : 'air';

    const where = document.createElement('span');
    where.textContent = a.pos.join(', ');

    const cell = document.createElement('span');
    cell.appendChild(hashCell(tx.hash));

    row.append(kind, what, where, cell);
    feed.insertBefore(row, feed.firstChild);
  }

  trim(feed);
}

function kv(parent, key, value, cls) {
  const k = document.createElement('span');
  k.textContent = key;
  const v = document.createElement('span');
  v.textContent = value;
  if (cls) v.className = cls;
  parent.append(k, v);
}

async function showTx(hash, cell) {
  if (selected) selected.classList.remove('on');
  selected = cell;
  if (cell) cell.classList.add('on');

  const pane = el('tx');
  pane.textContent = 'looking up ' + short(hash) + '...';

  const res = await fetch('/api/tx?hash=' + hash);
  const body = await res.json();

  if (!res.ok) {
    pane.textContent = body.error;
    return;
  }

  const tx = body.tx;
  el('txNote').textContent = 'from ' + body.source;

  pane.textContent = '';

  const box = document.createElement('div');
  box.className = 'kv';
  kv(box, 'hash', tx.hash);
  kv(box, 'height', String(tx.height));
  kv(box, 'time', clock(tx.time));
  if (!tx.known) {
    kv(box, 'result', 'unknown, the node discarded its abci responses', 'hint');
  } else {
    kv(box, 'result', tx.code === 0 ? 'accepted' : 'rejected, code ' + tx.code,
       tx.code === 0 ? 'ok' : 'fail');
  }
  if (tx.log) kv(box, 'log', tx.log, 'fail');
  kv(box, 'signer', tx.signer);
  kv(box, 'sequence', String(tx.sequence));
  kv(box, 'gas', (tx.known ? tx.gasUsed : '?') + ' of ' + tx.gasWanted);
  kv(box, 'fee', tx.fee || 'none');
  kv(box, 'size', tx.size + ' bytes');
  kv(box, 'contains', summarise(tx));
  pane.appendChild(box);

  const h = document.createElement('h3');
  h.textContent = 'MsgTick actions';
  pane.appendChild(h);

  const list = document.createElement('ol');
  for (const a of tx.actions) {
    const li = document.createElement('li');
    if (a.kind === 'break') li.textContent = 'break ' + a.pos.join(', ');
    else if (a.kind === 'place') li.textContent = 'place ' + a.block + ' at ' + a.pos.join(', ');
    else if (a.kind === 'move') li.textContent = 'move ' + a.at.map((n) => n.toFixed(2)).join(', ');
    else if (a.kind === 'chat') li.textContent = 'chat ' + a.text;
    else if (a.kind === 'hotbar') li.textContent = 'hotbar slot ' + a.slot;
    else li.textContent = a.kind;
    list.appendChild(li);
  }
  pane.appendChild(list);
}

function applyState(s) {
  el('tick').textContent = s.tick;
  el('statehash').textContent = short(s.stateHash);
  el('player').textContent = s.address
    ? s.player.map((n) => n.toFixed(1)).join(', ')
    : 'nobody';
}

async function start() {
  const head = await (await fetch('/api/head')).json();

  el('chain').textContent = head.chainId || '-';
  applyState(head.state);

  // Seeded oldest first, so each one lands on top of the last.
  for (const b of head.blocks.slice().reverse()) addBlock(b);

  const stream = new EventSource('/api/stream');
  stream.addEventListener('block', (e) => addBlock(JSON.parse(e.data)));
  stream.addEventListener('state', (e) => applyState(JSON.parse(e.data)));
  stream.onerror = () => { el('blockNote').textContent = 'stream dropped, reconnecting'; };
  stream.onopen = () => { el('blockNote').textContent = 'streaming'; };
}

start();
