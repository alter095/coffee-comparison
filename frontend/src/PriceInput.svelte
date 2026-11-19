<script lang="ts">
  import { untrack } from 'svelte';
  import type { PriceEditor, EditorState } from './lib/autosave.ts';
  import { yen } from './lib/cost.ts';
  let {
    editor,
    label,
    onDiscardDeleted,
  }: {
    editor: PriceEditor;
    label: string;
    onDiscardDeleted?: () => void | Promise<void>;
  } = $props();
  let view = $state<EditorState>(untrack(() => editor.snapshot()));
  $effect(() => editor.subscribe((next) => (view = next)));
  const messages = {
    saved: '保存済み',
    editing: '未保存',
    saving: '保存中…',
    error: '保存失敗・入力は保持しています',
    conflict: '他の端末で変更されました',
    invalid: '0〜1,000,000の整数を入力',
    deleted: '保存先のデータは削除されています。入力は保持しています',
  };
</script>

<div
  class="price-editor"
  class:attention={['error', 'conflict', 'invalid', 'deleted'].includes(
    view.status,
  )}
>
  <div class="price-box">
    <span aria-hidden="true">¥</span><input
      aria-label={label}
      type="text"
      inputmode="numeric"
      value={view.draft}
      readonly={view.status === 'deleted'}
      oninput={(e) => editor.edit(e.currentTarget.value)}
      onblur={() => editor.commit()}
      onkeydown={(e) => {
        if (e.key === 'Enter' && !e.isComposing) {
          e.preventDefault();
          void editor.commit();
        }
      }}
    />
  </div>
  <div
    class="save-state"
    class:sr-only={view.status === 'saved'}
    aria-live="polite"
  >
    {messages[view.status]}
  </div>
  {#if view.status === 'error'}<button
      class="text-button"
      onclick={() => editor.commit()}>再試行</button
    >{/if}
  {#if view.status === 'deleted' && onDiscardDeleted}<button
      class="text-button"
      onclick={() => onDiscardDeleted?.()}
      >この下書きを破棄して一覧を更新</button
    >{/if}
  {#if view.status === 'conflict' && view.current}
    <div class="conflict-box">
      保存先の価格: {yen(view.current.price_yen)}（{view.current.weight_g}g）
      <button class="text-button" onclick={() => editor.useCurrent()}
        >保存先の値を使う</button
      >
      <button class="text-button" onclick={() => editor.reapply()}
        >入力値で再保存</button
      >
    </div>
  {/if}
</div>
