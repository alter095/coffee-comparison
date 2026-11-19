<script lang="ts">
  import type { Shop } from './lib/types.ts';
  let {
    shops,
    value,
    disabled = false,
    onchange,
  }: {
    shops: Shop[];
    value: string;
    disabled?: boolean;
    onchange: (id: string) => void;
  } = $props();
  let dialog: HTMLDialogElement;
  let searchInput: HTMLInputElement;
  let query = $state('');
  let limit = $state(30);
  const normalize = (s: string) =>
    s.normalize('NFKC').toLocaleLowerCase().replace(/\s+/g, '');
  let results = $derived(
    shops
      .filter((s) => normalize(s.name).includes(normalize(query)))
      .sort((a, b) => b.id - a.id),
  );
  function choose(id: string) {
    onchange(id);
    dialog.close();
  }
</script>

<div class="wide">
  <span class="field-label">店舗</span>
  <button
    type="button"
    class="secondary shop-choice"
    {disabled}
    onclick={() => {
      query = '';
      limit = 30;
      dialog.showModal();
      searchInput.focus();
    }}
  >
    {shops.find((s) => String(s.id) === value)?.name ??
      '登録済みの店舗を検索・選択'}
  </button>
  <dialog
    bind:this={dialog}
    aria-labelledby="shop-picker-title"
    class="shop-picker"
  >
    <div class="panel-heading">
      <h2 id="shop-picker-title">店舗を選択</h2>
      <button type="button" class="secondary" onclick={() => dialog.close()}
        >戻る</button
      >
    </div>
    <label
      >店舗名を検索<input
        type="search"
        bind:this={searchInput}
        onkeydown={(e) => {
          if (e.key === 'Enter' && !e.isComposing) e.preventDefault();
        }}
        bind:value={query}
        oninput={() => (limit = 30)}
      /></label
    >
    <p role="status">{results.length}件</p>
    <div class="shop-results">
      {#each results.slice(0, limit) as shop (shop.id)}
        <button
          type="button"
          class="secondary"
          aria-pressed={value === String(shop.id)}
          onclick={() => choose(String(shop.id))}
        >
          <strong>{shop.name}</strong><span
            >送料 {shop.shipping_yen === null
              ? '未設定'
              : `${shop.shipping_yen.toLocaleString()}円`}{shop.free_shipping_threshold_yen !==
            null
              ? `・${shop.free_shipping_threshold_yen.toLocaleString()}円以上で無料`
              : ''}</span
          >
        </button>
      {/each}
      {#if results.length > limit}<button
          type="button"
          class="secondary"
          onclick={() => (limit += 30)}>さらに表示</button
        >{/if}
      {#if !results.length}<p>
          一致する店舗がありません。検索語を変えるか、新しい店舗を登録できます。
        </p>{/if}
    </div>
    <button type="button" class="secondary" onclick={() => choose('')}
      >新しい店舗名を入力</button
    >
  </dialog>
</div>
