import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PriceEditor, ConflictError } from './autosave.ts';
import { request } from './client.ts';
import type { Variant } from './types.ts';
const original: Variant = {
  id: 1,
  bean_id: 1,
  weight_g: 200,
  price_yen: 1600,
  version: 1,
};

test('failed save preserves input and can be retried', async () => {
  let fail = true;
  const editor = new PriceEditor(original, async (version, price) => {
    if (fail) throw new Error('offline');
    return { ...original, price_yen: price, version: version + 1 };
  });
  editor.edit('1750');
  await editor.commit();
  assert.equal(editor.snapshot().status, 'error');
  assert.equal(editor.snapshot().draft, '1750');
  assert.equal(editor.snapshot().saved.price_yen, 1600);
  fail = false;
  await editor.commit();
  assert.equal(editor.snapshot().status, 'saved');
  assert.equal(editor.snapshot().saved.price_yen, 1750);
});

test('an old list response cannot roll back a confirmed save', async () => {
  const editor = new PriceEditor(original, async (version, price) => ({
    ...original,
    price_yen: price,
    version: version + 1,
  }));
  editor.edit('1750');
  await editor.commit();
  editor.reconcile(original);
  assert.equal(editor.snapshot().saved.version, 2);
  assert.equal(editor.snapshot().saved.price_yen, 1750);
  assert.equal(editor.snapshot().draft, '1750');
  assert.equal(editor.snapshot().status, 'saved');
  assert.equal(editor.hasPending(), false);

  editor.reconcile({ ...original, price_yen: 1900, version: 3 });
  assert.equal(editor.snapshot().saved.version, 3);
  assert.equal(editor.snapshot().saved.price_yen, 1900);
  assert.equal(editor.snapshot().draft, '1900');
});

test('a duplicate queued confirmation cannot overwrite a newer refreshed price', async () => {
  const remote = { ...original, price_yen: 1900, version: 3 };
  const calls: [number, number][] = [];
  let resolve!: (v: Variant) => void;
  let editor!: PriceEditor;
  editor = new PriceEditor(
    original,
    async (version, price) => {
      calls.push([version, price]);
      if (calls.length === 1) return new Promise<Variant>((r) => (resolve = r));
      return { ...original, price_yen: price, version: version + 1 };
    },
    () => editor.reconcile(remote),
  );
  editor.edit('1700');
  const saving = editor.commit();
  await editor.commit();
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.deepEqual(calls, [[1, 1700]]);
  assert.equal(editor.snapshot().saved.version, 3);
  assert.equal(editor.snapshot().saved.price_yen, 1900);
  assert.equal(editor.snapshot().draft, '1900');
  assert.equal(editor.snapshot().status, 'saved');
});

test('a different queued confirmation retains its base version despite a newer refresh', async () => {
  const remote = { ...original, price_yen: 1900, version: 3 };
  const calls: [number, number][] = [];
  let resolve!: (v: Variant) => void;
  let editor!: PriceEditor;
  editor = new PriceEditor(
    original,
    async (version, price) => {
      calls.push([version, price]);
      if (calls.length === 1) return new Promise<Variant>((r) => (resolve = r));
      if (version === 2) throw new ConflictError(remote);
      return { ...original, price_yen: price, version: version + 1 };
    },
    () => editor.reconcile(remote),
  );
  editor.edit('1700');
  const saving = editor.commit();
  editor.edit('1800');
  await editor.commit();
  editor.edit('1700');
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.deepEqual(calls, [
    [1, 1700],
    [2, 1800],
  ]);
  assert.equal(editor.snapshot().saved.version, 2);
  assert.equal(editor.snapshot().status, 'conflict');
  assert.equal(editor.snapshot().current?.price_yen, 1900);
  assert.equal(editor.snapshot().draft, '1700');
});

test('a late response cannot overwrite a newer draft; sends are serialized', async () => {
  const calls: [number, number][] = [];
  let resolve!: (v: Variant) => void;
  const editor = new PriceEditor(original, async (version, price) => {
    calls.push([version, price]);
    if (calls.length === 1) return new Promise<Variant>((r) => (resolve = r));
    return { ...original, price_yen: price, version: version + 1 };
  });
  editor.edit('1700');
  const saving = editor.commit();
  editor.edit('1800');
  await editor.commit();
  assert.deepEqual(calls, [[1, 1700]]);
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.deepEqual(calls, [
    [1, 1700],
    [2, 1800],
  ]);
  assert.equal(editor.snapshot().draft, '1800');
  assert.equal(editor.snapshot().saved.price_yen, 1800);
});

test('conflict keeps local input and never retries without an explicit choice', async () => {
  let calls = 0;
  const remote = { ...original, price_yen: 1900, version: 2 };
  const editor = new PriceEditor(original, async (version, price) => {
    calls++;
    if (version === 1) throw new ConflictError(remote);
    return { ...remote, price_yen: price, version: 3 };
  });
  editor.edit('1750');
  await editor.commit();
  await editor.commit();
  assert.equal(calls, 1);
  assert.equal(editor.snapshot().draft, '1750');
  assert.equal(editor.snapshot().current?.price_yen, 1900);
  await editor.reapply();
  assert.equal(calls, 2);
  assert.equal(editor.snapshot().saved.price_yen, 1750);
});

test('invalid or unchanged input never sends a request', async () => {
  let calls = 0;
  const editor = new PriceEditor(original, async () => {
    calls++;
    return original;
  });
  for (const value of ['', '-1', '1.5', '1000001', '1e3', 'abc']) {
    editor.edit(value);
    await editor.commit();
    assert.equal(editor.snapshot().status, 'invalid');
  }
  editor.edit('1600');
  await editor.commit();
  assert.equal(calls, 0);
});

test('a lost response must not mark the old price saved without checking the server', async () => {
  let remote = { ...original };
  const calls: [number, number][] = [];
  const editor = new PriceEditor(original, async (version, price) => {
    calls.push([version, price]);
    if (version !== remote.version) throw new ConflictError(remote);
    remote = { ...remote, price_yen: price, version: remote.version + 1 };
    if (calls.length === 1) throw new Error('response lost after commit');
    return remote;
  });
  editor.edit('1750');
  await editor.commit();
  assert.equal(remote.price_yen, 1750);
  editor.edit('1600');
  await editor.commit();
  assert.equal(editor.snapshot().status, 'conflict');
  assert.equal(editor.snapshot().draft, '1600');
  assert.equal(editor.snapshot().current?.price_yen, 1750);
  assert.deepEqual(calls, [
    [1, 1750],
    [1, 1600],
  ]);
  await editor.reapply();
  assert.equal(remote.price_yen, 1600);
  assert.equal(editor.snapshot().status, 'saved');
  assert.equal(editor.snapshot().saved.version, remote.version);
});

test('returning to the old price after a pre-write failure is also verified', async () => {
  let calls = 0;
  const editor = new PriceEditor(original, async (version, price) => {
    if (++calls === 1) throw new Error('connection failed before write');
    return { ...original, price_yen: price, version: version + 1 };
  });
  editor.edit('1750');
  await editor.commit();
  editor.edit('1600');
  await editor.commit();
  assert.equal(calls, 2);
  assert.equal(editor.snapshot().status, 'saved');
  assert.equal(editor.snapshot().saved.version, 2);
});

test('the queue sends only the last confirmed value, preserving a later unconfirmed draft', async () => {
  const calls: [number, number][] = [];
  let resolve!: (v: Variant) => void;
  const editor = new PriceEditor(original, async (version, price) => {
    calls.push([version, price]);
    if (calls.length === 1) return new Promise<Variant>((r) => (resolve = r));
    return { ...original, price_yen: price, version: version + 1 };
  });
  editor.edit('1700');
  const saving = editor.commit();
  editor.edit('1800');
  await editor.commit();
  editor.edit('2'); // The user has started typing 2000, without confirming it.
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.deepEqual(calls, [
    [1, 1700],
    [2, 1800],
  ]);
  assert.equal(editor.snapshot().saved.price_yen, 1800);
  assert.equal(editor.snapshot().draft, '2');
  assert.equal(editor.snapshot().status, 'editing');
  await editor.commit();
  assert.deepEqual(calls[2], [3, 2]);
});

test('an unchanged queued confirmation does not mark a newer draft as saved', async () => {
  let resolve!: (v: Variant) => void;
  const calls: [number, number][] = [];
  const editor = new PriceEditor(original, async (version, price) => {
    calls.push([version, price]);
    return new Promise<Variant>((r) => (resolve = r));
  });
  editor.edit('1700');
  const saving = editor.commit();
  await editor.commit();
  editor.edit('1800');
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.deepEqual(calls, [[1, 1700]]);
  assert.equal(editor.snapshot().draft, '1800');
  assert.equal(editor.snapshot().status, 'editing');
});

test('multiple queued confirmations coalesce without including later typing', async () => {
  let resolve!: (v: Variant) => void;
  const calls: number[] = [];
  const editor = new PriceEditor(original, async (version, price) => {
    calls.push(price);
    if (calls.length === 1) return new Promise<Variant>((r) => (resolve = r));
    return { ...original, price_yen: price, version: version + 1 };
  });
  editor.edit('1700');
  const saving = editor.commit();
  editor.edit('1800');
  await editor.commit();
  editor.edit('1900');
  await editor.commit();
  editor.edit('20');
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.deepEqual(calls, [1700, 1900]);
  assert.equal(editor.snapshot().draft, '20');
  assert.equal(editor.snapshot().status, 'editing');
});

test('an invalid queued confirmation cancels the older queued value', async () => {
  let resolve!: (v: Variant) => void;
  const calls: number[] = [];
  const editor = new PriceEditor(original, async (version, price) => {
    calls.push(price);
    return new Promise<Variant>((r) => (resolve = r));
  });
  editor.edit('1700');
  const saving = editor.commit();
  editor.edit('1800');
  await editor.commit();
  editor.edit('');
  await editor.commit();
  editor.edit('2');
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.deepEqual(calls, [1700]);
  assert.equal(editor.snapshot().draft, '2');
  assert.equal(editor.snapshot().status, 'editing');
});

test('a deleted variant keeps its draft and stops retries until explicitly discarded', async (t) => {
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    return new Response(JSON.stringify({ error: 'record not found' }), {
      status: 404,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  const editor = new PriceEditor(original, (version, price) =>
    request<Variant>('/api/variants/1', 'PATCH', { version, price_yen: price }),
  );
  editor.edit('1750');
  await editor.commit();
  assert.equal(editor.snapshot().status, 'deleted');
  assert.equal(editor.snapshot().draft, '1750');
  assert.equal(editor.hasPending(), true);
  await editor.commit();
  assert.equal(calls, 1);
  editor.discardDeleted();
  assert.equal(editor.hasPending(), false);
  assert.equal(editor.snapshot().draft, '1600');
});

test('a deletion rejects queued saves and discard cannot clear an unrelated draft', async (t) => {
  let complete!: (response: Response) => void;
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async () => {
    calls++;
    return new Promise<Response>((resolve) => (complete = resolve));
  });
  const editor = new PriceEditor(original, (version, price) =>
    request<Variant>('/api/variants/1', 'PATCH', { version, price_yen: price }),
  );
  const other = new PriceEditor({ ...original, id: 2 }, async () => original);
  other.edit('1800');
  editor.edit('1700');
  const saving = editor.commit();
  editor.edit('1750');
  await editor.commit();
  complete(new Response('{}', { status: 404 }));
  await saving;
  assert.equal(editor.snapshot().status, 'deleted');
  assert.equal(editor.snapshot().draft, '1750');
  assert.equal(calls, 1);
  other.discardDeleted();
  assert.equal(other.snapshot().draft, '1800');
  assert.equal(other.hasPending(), true);
});

test('a refresh can mark a pending draft deleted without a late response reviving it', async () => {
  let resolve!: (variant: Variant) => void;
  const editor = new PriceEditor(
    original,
    async () => new Promise<Variant>((complete) => (resolve = complete)),
  );
  editor.edit('1700');
  const saving = editor.commit();
  editor.edit('1750');
  await editor.commit();
  editor.markDeleted();
  assert.equal(editor.snapshot().status, 'deleted');
  assert.equal(editor.snapshot().draft, '1750');
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.equal(editor.snapshot().status, 'deleted');
  assert.equal(editor.snapshot().draft, '1750');
  assert.equal(editor.snapshot().saved.price_yen, 1600);
});

test('marking a deleted pending draft never changes an editor without pending input', () => {
  const editor = new PriceEditor(original, async () => original);
  editor.markDeleted();
  assert.equal(editor.snapshot().status, 'saved');
  assert.equal(editor.hasPending(), false);
  editor.edit('1750');
  editor.markDeleted();
  assert.equal(editor.snapshot().status, 'deleted');
  assert.equal(editor.snapshot().draft, '1750');
});

test('discarding a deleted editor cannot revive a request that is still in flight', async () => {
  let resolve!: (variant: Variant) => void;
  let savedCalls = 0;
  const editor = new PriceEditor(
    original,
    async () => new Promise<Variant>((complete) => (resolve = complete)),
    () => savedCalls++,
  );
  editor.edit('1700');
  const saving = editor.commit();
  editor.markDeleted();
  editor.discardDeleted();
  resolve({ ...original, price_yen: 1700, version: 2 });
  await saving;
  assert.equal(savedCalls, 0);
  assert.equal(editor.snapshot().saved.price_yen, 1600);
  assert.equal(editor.snapshot().draft, '1600');
  assert.equal(editor.hasPending(), false);
});
