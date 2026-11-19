<script lang="ts">
  import { untrack, onMount } from 'svelte';
  import type { Bean, Variant } from './lib/types.ts';
  import { request } from './lib/client.ts';
  let {
    bean,
    disabled,
    onchanged,
    onfailure,
  }: {
    bean: Bean;
    disabled: boolean;
    onchanged: () => Promise<unknown>;
    onfailure: (e: unknown) => void;
  } = $props();
  let name = $state(untrack(() => bean.name)),
    url = $state(untrack(() => bean.product_url ?? ''));
  let dirty = $state(false);
  onMount(() => {
    const guard = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault();
        e.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', guard);
    return () => window.removeEventListener('beforeunload', guard);
  });
  let busy = $state(false),
    error = $state('');
  async function save(event: SubmitEvent, variant?: Variant) {
    event.preventDefault();
    if (busy || disabled) return;
    const data = new FormData(event.currentTarget as HTMLFormElement);
    busy = true;
    error = '';
    try {
      await request(
        variant ? `/api/variants/${variant.id}` : `/api/beans/${bean.id}`,
        'PATCH',
        variant
          ? {
              version: variant.version,
              weight_g: Number(data.get('weight')),
              price_yen: Number(data.get('price')),
            }
          : { version: bean.version, name, product_url: url || null },
      );
      dirty = false;
      await onchanged();
    } catch (e) {
      onfailure(e);
      error = e instanceof Error ? e.message : '保存できませんでした。';
    } finally {
      busy = false;
    }
  }
  async function remove(variant?: Variant) {
    if (
      busy ||
      disabled ||
      !window.confirm(
        variant
          ? `${variant.weight_g}gの容量を削除します。元に戻せません。`
          : `${bean.name}と全容量を削除します。元に戻せません。`,
      )
    )
      return;
    busy = true;
    error = '';
    try {
      await request(
        variant ? `/api/variants/${variant.id}` : `/api/beans/${bean.id}`,
        'DELETE',
        { version: variant?.version ?? bean.version },
      );
      dirty = false;
      await onchanged();
    } catch (e) {
      onfailure(e);
      error = e instanceof Error ? e.message : '削除できませんでした。';
    } finally {
      busy = false;
    }
  }
</script>

<section
  oninput={() => (dirty = true)}
  class="bean-manager"
  aria-label={`${bean.name} の編集`}
>
  <h4>豆・容量の編集</h4>
  <p>
    価格の未保存分を解決してから編集できます。競合時は入力を控え、比較を更新して最新の内容を確認してください。
  </p>
  {#if error}<p class="alert" role="alert">{error}</p>{/if}
  <form class="form-grid" onsubmit={(e) => save(e)}>
    <fieldset disabled={busy || disabled}>
      <label class="wide"
        >豆の名前<input required maxlength="200" bind:value={name} /></label
      >
      <label class="wide"
        >商品URL<input type="url" maxlength="4096" bind:value={url} /></label
      >
      <button class="secondary">豆の情報を保存</button>
      <button type="button" class="secondary danger" onclick={() => remove()}
        >豆を削除</button
      >
    </fieldset>
  </form>
  {#each bean.variants as variant (variant.id + ':' + variant.version)}
    <form
      class="form-grid"
      aria-label={`${variant.weight_g}g の容量編集`}
      onsubmit={(e) => save(e, variant)}
    >
      <fieldset disabled={busy || disabled}>
        <label
          >容量（g）<input
            name="weight"
            type="number"
            inputmode="numeric"
            min="1"
            max="1000000"
            required
            value={variant.weight_g}
          /></label
        >
        <label
          >価格（円）<input
            name="price"
            type="number"
            inputmode="numeric"
            min="0"
            max="1000000"
            required
            value={variant.price_yen}
          /></label
        >
        <button class="secondary">容量を保存</button>
        <button
          type="button"
          class="secondary danger"
          onclick={() => remove(variant)}>この容量を削除</button
        >
      </fieldset>
    </form>
  {/each}
</section>
