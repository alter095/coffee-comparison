import type { Variant } from './types.ts';
export type SaveStatus =
  'saved' | 'editing' | 'saving' | 'error' | 'conflict' | 'invalid' | 'deleted';
export interface EditorState {
  draft: string;
  saved: Variant;
  status: SaveStatus;
  current: Variant | null;
}
export class ConflictError extends Error {
  current: Variant;
  constructor(current: Variant) {
    super('price conflict');
    this.current = current;
  }
}
export class NotFoundError extends Error {
  constructor() {
    super('保存先のデータは削除されています。');
    this.name = 'NotFoundError';
  }
}
export function parsePrice(value: string): number | null {
  if (!/^\d+$/.test(value)) return null;
  const result = Number(value);
  return Number.isSafeInteger(result) && result <= 1_000_000 ? result : null;
}
export class PriceEditor {
  private state: EditorState;
  private listeners = new Set<(state: EditorState) => void>();
  private send: (version: number, price: number) => Promise<Variant>;
  private onSaved: () => void;
  private queued: string | null = null;
  // Stable IDs are never reused. Once removed, this editor must ignore every
  // outstanding response even after the user discards its pending draft.
  private deleted = false;
  // A failed response cannot tell us whether the server committed the write.
  private uncertain = false;
  constructor(
    saved: Variant,
    send: (version: number, price: number) => Promise<Variant>,
    onSaved = () => {},
  ) {
    this.state = {
      saved: { ...saved },
      draft: String(saved.price_yen),
      status: 'saved',
      current: null,
    };
    this.send = send;
    this.onSaved = onSaved;
  }
  snapshot(): EditorState {
    return { ...this.state, saved: { ...this.state.saved } };
  }
  subscribe(listener: (state: EditorState) => void): () => void {
    this.listeners.add(listener);
    listener(this.snapshot());
    return () => {
      this.listeners.delete(listener);
    };
  }
  private emit() {
    for (const listener of this.listeners) listener(this.snapshot());
  }
  hasPending(): boolean {
    return this.state.status !== 'saved';
  }
  private isDeleted(): boolean {
    return this.deleted;
  }
  markDeleted() {
    if (!this.hasPending() || this.isDeleted()) return;
    this.deleted = true;
    this.state.status = 'deleted';
    this.state.current = null;
    this.queued = null;
    this.emit();
  }
  edit(draft: string) {
    if (this.isDeleted()) return;
    this.state.draft = draft;
    if (this.state.status !== 'saving' && this.state.status !== 'conflict')
      this.state.status = 'editing';
    this.emit();
  }
  reconcile(saved: Variant) {
    if (
      this.hasPending() ||
      this.isDeleted() ||
      this.queued !== null ||
      saved.version < this.state.saved.version
    )
      return;
    this.state.saved = { ...saved };
    this.state.draft = String(saved.price_yen);
    this.emit();
  }
  async commit(): Promise<void> {
    if (this.state.status === 'conflict' || this.isDeleted()) return;
    const draft = this.state.draft;
    if (this.state.status === 'saving') {
      // Keep the confirmed value, not a flag that would reread a later draft.
      this.queued = draft;
      return;
    }
    await this.save(draft);
  }
  private async save(draft: string): Promise<void> {
    const price = parsePrice(draft);
    if (price === null) {
      this.state.status = this.state.draft === draft ? 'invalid' : 'editing';
      this.emit();
      return;
    }
    if (!this.uncertain && price === this.state.saved.price_yen) {
      this.state.status =
        parsePrice(this.state.draft) === this.state.saved.price_yen
          ? 'saved'
          : 'editing';
      this.emit();
      return;
    }
    this.state.status = 'saving';
    this.emit();
    try {
      const saved = await this.send(this.state.saved.version, price);
      // A refresh may discover a deletion while this request is in flight.
      if (this.isDeleted()) return;
      this.state.saved = { ...saved };
      this.uncertain = false;
      if (this.state.draft === draft)
        this.state.draft = String(saved.price_yen);
      this.state.status =
        parsePrice(this.state.draft) === saved.price_yen ? 'saved' : 'editing';
      // The confirmed request already covered a numerically identical queue.
      // Clear it before onSaved can reconcile a newer server snapshot.
      if (this.queued !== null && parsePrice(this.queued) === saved.price_yen)
        this.queued = null;
      this.onSaved();
    } catch (error) {
      if (this.isDeleted()) return;
      if (error instanceof NotFoundError) {
        this.deleted = true;
        this.state.current = null;
        this.state.status = 'deleted';
      } else if (error instanceof ConflictError) {
        this.state.current = error.current;
        this.state.status = 'conflict';
      } else {
        this.uncertain = true;
        this.state.status = 'error';
      }
    }
    const queued = this.queued;
    this.queued = null;
    this.emit();
    if (
      queued !== null &&
      this.state.status !== 'conflict' &&
      this.state.status !== 'deleted' &&
      this.state.status !== 'error'
    )
      await this.save(queued);
  }
  discardDeleted() {
    if (this.state.status !== 'deleted') return;
    this.state.draft = String(this.state.saved.price_yen);
    this.state.current = null;
    this.state.status = 'saved';
    this.queued = null;
    this.uncertain = false;
    this.emit();
  }
  useCurrent() {
    if (!this.state.current) return;
    this.state.saved = { ...this.state.current };
    this.uncertain = false;
    this.state.current = null;
    this.state.draft = String(this.state.saved.price_yen);
    this.state.status = 'saved';
    this.emit();
  }
  async reapply() {
    if (!this.state.current) return;
    this.state.saved = { ...this.state.current };
    this.uncertain = false;
    this.state.current = null;
    this.state.status = 'editing';
    await this.commit();
  }
}
