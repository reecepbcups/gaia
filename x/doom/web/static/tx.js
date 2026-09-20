// Transaction encoding for the DOOM client.
//
// Enough hand-rolled protobuf to sign a MsgInput in SIGN_MODE_DIRECT, which is
// cheaper than dragging a bundler and a proto toolchain into the repo for one
// message with two fields.

import { getPublicKey, signAsync } from './secp256k1.js';

export const MSG_INPUT = '/gaia.doom.v1.MsgInput';
const PUBKEY_SECP256K1 = '/cosmos.crypto.secp256k1.PubKey';
const SIGN_MODE_DIRECT = 1;
const GAS_LIMIT = 200000n;
// The chain's minimum gas price is set so that GAS_LIMIT rounds up to exactly
// one unit. 35 of those a second is what playing costs.
const FEE_AMOUNT = '1';

function varint(n) {
  let v = BigInt(n);
  const out = [];
  do {
    let b = Number(v & 0x7fn);
    v >>= 7n;
    if (v > 0n) b |= 0x80;
    out.push(b);
  } while (v > 0n);
  return Uint8Array.from(out);
}

function concat(parts) {
  const len = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(len);
  let off = 0;
  for (const p of parts) { out.set(p, off); off += p.length; }
  return out;
}

// field, wire type 2
function bytesField(field, value) {
  if (!value || value.length === 0) return new Uint8Array(0);
  return concat([varint((field << 3) | 2), varint(value.length), value]);
}

function stringField(field, value) {
  if (!value) return new Uint8Array(0);
  return bytesField(field, new TextEncoder().encode(value));
}

// field, wire type 0
function varintField(field, value) {
  if (!value) return new Uint8Array(0);
  return concat([varint((field << 3) | 0), varint(value)]);
}

function any(typeUrl, value) {
  return concat([stringField(1, typeUrl), bytesField(2, value)]);
}

//
// transaction assembly
//

function encodeMsgInput(player, buttons) {
  return concat([stringField(1, player), varintField(2, buttons)]);
}

function encodeTxBody(msgAny) {
  return bytesField(1, msgAny);
}

function encodeAuthInfo(pubkey, sequence, feeDenom) {
  // ModeInfo { single { mode: DIRECT } }
  const single = varintField(1, SIGN_MODE_DIRECT);
  const modeInfo = bytesField(1, single);

  const signerInfo = concat([
    bytesField(1, any(PUBKEY_SECP256K1, bytesField(1, pubkey))),
    bytesField(2, modeInfo),
    varintField(3, sequence),
  ]);

  const coin = concat([stringField(1, feeDenom), stringField(2, FEE_AMOUNT)]);
  const fee = concat([bytesField(1, coin), varintField(2, GAS_LIMIT)]);

  return concat([bytesField(1, signerInfo), bytesField(2, fee)]);
}

function encodeSignDoc(bodyBytes, authInfoBytes, chainId, accountNumber) {
  return concat([
    bytesField(1, bodyBytes),
    bytesField(2, authInfoBytes),
    stringField(3, chainId),
    varintField(4, accountNumber),
  ]);
}

function encodeTxRaw(bodyBytes, authInfoBytes, signature) {
  return concat([
    bytesField(1, bodyBytes),
    bytesField(2, authInfoBytes),
    bytesField(3, signature),
  ]);
}

function hexToBytes(hex) {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.substr(i * 2, 2), 16);
  return out;
}

function toBase64(bytes) {
  let s = '';
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s);
}

async function sha256(bytes) {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', bytes));
}


// signInput builds and signs one MsgInput. session is what /api/session
// returned; sequence moves independently because it changes every transaction.
export async function signInput({ session, privKey, pubKey, sequence, buttons }) {
  const msg = encodeMsgInput(session.address, buttons);
  const bodyBytes = encodeTxBody(any(MSG_INPUT, msg));
  const authInfoBytes = encodeAuthInfo(pubKey, sequence, session.feeDenom);
  const signDoc = encodeSignDoc(bodyBytes, authInfoBytes, session.chainId, BigInt(session.accountNumber));

  const sig = await signAsync(await sha256(signDoc), privKey);
  return toBase64(encodeTxRaw(bodyBytes, authInfoBytes, sig.toCompactRawBytes()));
}

export { getPublicKey, hexToBytes, concat };
