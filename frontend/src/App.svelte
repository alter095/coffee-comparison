<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { copy } from './lib/copy.ts';
  import PriceInput from './PriceInput.svelte';
  import ShopPicker from './ShopPicker.svelte';
  import BeanManager from './BeanManager.svelte';
  import { PriceEditor, parsePrice } from './lib/autosave.ts';
  import { login, logout, request, UnauthorizedError } from './lib/client.ts';
  import { compare, cost, maxQuantity, yen } from './lib/cost.ts';
  import {
    maxTargetWeight,
    planPurchase,
    type PurchasePlan,
  } from './lib/weight-plan.ts';
  import type { Bean, Shop, Variant } from './lib/types.ts';

  let beans = $state<Bean[]>([]),
    shops = $state<Shop[]>([]);
  let newestShops = $derived([...shops].sort((a, b) => b.id - a.id));
  let loading = $state(true),
    submitting = $state(false),
    error = $state(''),
    authRequired = $state(false),
    loginSubmitting = $state(false),
    loginError = $state('');
  let authenticated = $state(false);
  async function signOut() {
    if (locked || addDirty || shopDirty) return;
    submitting = true;
    error = '';
    try {
      await logout();
      authenticated = false;
      authRequired = true;
    } catch (e) {
      error = message(e);
    } finally {
      submitting = false;
    }
  }
  let search = $state(''),
    sort = $state('unit'),
    statusFilter = $state('candidate');
  let budget = $state<number | undefined>(),
    minimumWeight = $state<number | undefined>(),
    maximumWeight = $state<number | undefined>();
  let conditionsOpen = $state(false);
  let managing = $state<Record<number, boolean>>({});
  let draft = $state({
    search: '',
    sort: 'unit',
    status: 'candidate',
    target: undefined as number | undefined,
    budget: undefined as number | undefined,
    min: undefined as number | undefined,
    max: undefined as number | undefined,
  });
  let appliedTarget = $state<number | undefined>();
  let trialSignature = '';
  let conditionsInvalid = $state(false);
  let conditionsDirty = $derived(
    conditionsInvalid ||
      JSON.stringify(draft) !==
        JSON.stringify({
          search,
          sort,
          status: statusFilter,
          target: appliedTarget,
          budget,
          min: minimumWeight,
          max: maximumWeight,
        }),
  );
  async function compareNow(event?: SubmitEvent) {
    event?.preventDefault();
    if (loading || submitting || pendingBean !== null) return;
    if (
      [draft.target, draft.budget, draft.min, draft.max].some(
        (v) =>
          v !== undefined && (!Number.isInteger(v) || v < 0 || v > 1000000000),
      ) ||
      (draft.target !== undefined &&
        (draft.target < 1 || draft.target > maxTargetWeight)) ||
      (draft.min !== undefined &&
        draft.max !== undefined &&
        draft.min > draft.max)
    ) {
      error =
        '購入量は1〜1,000,000g、条件は0以上の整数で、容量の下限は上限以下にしてください。';
      return;
    }
    if (!(await load())) return;
    search = draft.search;
    sort = draft.sort;
    statusFilter = draft.status;
    budget = draft.budget;
    minimumWeight = draft.min;
    maximumWeight = draft.max;
    const signature = JSON.stringify([draft.target, draft.min, draft.max]);
    if (
      draft.target !== undefined &&
      (signature !== trialSignature ||
        beans.some((b) => !weightTrial?.results[b.id]))
    ) {
      targetWeight = draft.target;
      applyWeight({ preventDefault() {} } as SubmitEvent);
    } else if (draft.target === undefined && weightTrial)
      restoreWeightSelection();
    trialSignature = signature;
    appliedTarget = draft.target;
    applyComparison();
    conditionsOpen = false;
    await tick();
    document
      .getElementById('comparison-results')
      ?.focus({ preventScroll: true });
  }
  // undefined is an unavailable automatic plan; null is a cleared manual input.
  let quantities = $state<Record<number, number | null | undefined>>({});
  let quantityDrafts = $state<Record<number, string>>({});
  let filtering = $derived(
    budget !== undefined ||
      minimumWeight !== undefined ||
      maximumWeight !== undefined,
  );
  let filterError = $derived(
    [budget, minimumWeight, maximumWeight].some(
      (v) =>
        v !== undefined && (!Number.isInteger(v) || v < 0 || v > 1000000000),
    )
      ? '条件は0〜1,000,000,000の整数で入力してください。'
      : minimumWeight !== undefined &&
          maximumWeight !== undefined &&
          minimumWeight > maximumWeight
        ? '容量の下限は上限以下にしてください。'
        : '',
  );
  function clearFilters() {
    budget = minimumWeight = maximumWeight = undefined;
  }
  function quantity(bean: Bean) {
    return Object.hasOwn(quantities, bean.id)
      ? (quantities[bean.id] ?? null)
      : 1;
  }
  function quantityValue(bean: Bean) {
    return Object.hasOwn(quantityDrafts, bean.id)
      ? quantityDrafts[bean.id]
      : String(quantity(bean) ?? '');
  }
  function quantityInvalid(bean: Bean) {
    if (quantityUnavailable(bean)) return false;
    const text = quantityValue(bean);
    const value = Number(text);
    return (
      text === '' ||
      !Number.isInteger(value) ||
      value < 1 ||
      value > maxQuantity
    );
  }
  function quantityUnavailable(bean: Bean) {
    return (
      !Object.hasOwn(quantityDrafts, bean.id) &&
      quantities[bean.id] === undefined &&
      weightTrial?.results[bean.id]?.kind === 'unavailable'
    );
  }
  function quantityError(bean: Bean) {
    if (
      Object.hasOwn(quantityDrafts, bean.id) ||
      quantityUnavailable(bean) ||
      !quantityInvalid(bean)
    )
      return '';
    return quantity(bean) === null
      ? '個数を入力してください'
      : `1〜${maxQuantity}個で入力してください`;
  }
  function commitQuantity(beanId: number) {
    if (!Object.hasOwn(quantityDrafts, beanId)) return;
    const value = quantityDrafts[beanId];
    quantities[beanId] = value === '' ? null : Number(value);
    delete quantityDrafts[beanId];
  }
  let selected = $state<Record<number, number>>({}),
    expanded = $state<Record<number, boolean>>({});
  let targetWeight = $state<number | undefined>(300);
  let weightTrial = $state<{
    target: number;
    results: Record<number, PurchasePlan>;
    before: {
      selected: Record<number, number>;
      quantities: Record<number, number | null | undefined>;
      drafts: Record<number, string>;
    };
  } | null>(null);
  let targetError = $derived(
    targetWeight === undefined ||
      !Number.isInteger(targetWeight) ||
      targetWeight < 1 ||
      targetWeight > maxTargetWeight
      ? `希望重量は1〜${maxTargetWeight.toLocaleString('ja-JP')}gの整数で入力してください。`
      : '',
  );
  function snapshotSelection() {
    const snapshot = { ...selected };
    for (const bean of beans) {
      // Keep explicit choices, including those temporarily outside the range.
      // Freeze implicit defaults before a later package addition changes them.
      if (snapshot[bean.id] === undefined) {
        const variant = chosen(bean);
        if (variant) snapshot[bean.id] = variant.id;
      }
    }
    return snapshot;
  }
  function applyWeight(event: SubmitEvent) {
    event.preventDefault();
    if (loading || submitting || targetError || filterError) return;
    const target = targetWeight!;
    const before = weightTrial?.before ?? {
      selected: snapshotSelection(),
      quantities: { ...quantities },
      drafts: { ...quantityDrafts },
    };
    const results: Record<number, PurchasePlan> = {};
    for (const bean of beans) {
      const result = planPurchase(
        bean.variants
          .filter((v) => editors.get(v.id)?.snapshot().status !== 'deleted')
          .map(current)
          .filter((v) => matchesWeight(v.weight_g)),
        bean.shop,
        target,
      );
      results[bean.id] = result;
      if (result.kind === 'ready') {
        selected[bean.id] = result.variant.id;
        quantities[bean.id] = result.quantity;
      } else {
        quantities[bean.id] = undefined;
      }
      delete quantityDrafts[bean.id];
    }
    weightTrial = { target, results, before };
  }
  function restoreWeightSelection() {
    if (!weightTrial || loading || submitting) return;
    selected = { ...weightTrial.before.selected };
    quantities = { ...weightTrial.before.quantities };
    quantityDrafts = { ...weightTrial.before.drafts };
    weightTrial = null;
  }
  function weightMessage(bean: Bean, variant: Variant | null) {
    if (!weightTrial || quantityError(bean)) return '';
    const result = weightTrial.results[bean.id];
    if (!result) return '未試算です。「一括試算」で反映してください。';
    const count = quantity(bean);
    const target = weightTrial.target;
    const prefix = `希望${target.toLocaleString('ja-JP')}g・`;
    if (
      variant &&
      count !== null &&
      Number.isInteger(count) &&
      count >= 1 &&
      count <= maxQuantity
    ) {
      const difference = target - variant.weight_g * count;
      const changed =
        result.kind !== 'ready' ||
        result.variant.id !== variant.id ||
        result.quantity !== count;
      const quote = cost(variant, bean.shop, count);
      return (
        prefix +
        (difference >= 0
          ? `不足${difference.toLocaleString('ja-JP')}g`
          : `超過${(-difference).toLocaleString('ja-JP')}g`) +
        (changed ? '・選択を変更済み' : '') +
        (quote.total === null ? '・送料未設定のため総額未確定' : '') +
        (result.quantityLimited ? '・999個を超える容量は除外' : '')
      );
    }
    if (result.kind === 'unavailable') {
      return (
        prefix +
        (result.reason === 'too-heavy'
          ? '希望重量に収まる容量がありません'
          : result.reason === 'quantity-limit'
            ? '個数上限999個のため試算不可'
            : '試算できる容量がありません')
      );
    }
    return prefix + '個数・容量を確認してください。';
  }
  let revision = $state(0),
    chosenShop = $state('');
  let shopName = $state('');
  let allowSimilar = $state(false);
  const normalizeShop = (name: string) =>
    name.normalize('NFKC').toLocaleLowerCase().replace(/\s+/g, '');
  let similarShops = $derived(
    shopName.trim()
      ? shops.filter(
          (s) =>
            normalizeShop(s.name).includes(normalizeShop(shopName)) ||
            normalizeShop(shopName).includes(normalizeShop(s.name)),
        )
      : [],
  );
  let selectedShop = $derived(
    shops.find((shop) => String(shop.id) === chosenShop),
  );
  let screen = $state('list');
  let showAdd = $derived(screen === 'add');
  let addDirty = $state(false);
  let dirtyShops = $state<Record<number, boolean>>({});
  let shopDirty = $derived(Object.values(dirtyShops).some(Boolean));
  let notice = $state('');
  let shopNotice = $state('');
  $effect(() => {
    if (!shopNotice) return;
    const timeout = window.setTimeout(() => (shopNotice = ''), 4000);
    return () => window.clearTimeout(timeout);
  });
  function navigate(target: 'list' | 'add' | 'shops') {
    window.location.hash = target;
  }
  async function syncScreen(focus = true) {
    screen = ['add', 'shops'].includes(window.location.hash.slice(1))
      ? window.location.hash.slice(1)
      : 'list';
    if (screen !== 'shops') shopNotice = '';
    await tick();
    if (focus)
      document
        .getElementById(
          screen === 'add'
            ? pendingBean !== null
              ? 'add-title'
              : 'bean-name'
            : screen === 'shops'
              ? 'shops-title'
              : 'add-trigger',
        )
        ?.focus();
  }

  let pendingBean = $state<number | null>(null);
  let initialAttempt = $state<{ weight_g: number; price_yen: number } | null>(
    null,
  );
  let registrationConflict = $state<Variant | null>(null);
  let listVersion = $state(0);
  let scrollY = $state(0),
    inputFocused = $state(false);
  let editingVisible = $derived.by(() => {
    void scrollY;
    if (!inputFocused) return false;
    const rect = document.activeElement?.getBoundingClientRect();
    return !!rect && rect.bottom > 0 && rect.top < window.innerHeight;
  });
  function applyComparison() {
    listVersion++;
  }
  function backToTop() {
    document.getElementById('page-top')?.focus({ preventScroll: true });
    window.scrollTo({
      top: 0,
      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches
        ? 'instant'
        : 'smooth',
    });
  }
  const editors = new Map<number, PriceEditor>();
  const subscriptions = new Map<number, () => void>();
  let pending = $derived.by(() => {
    void revision;
    return [...editors.values()].filter((e) => e.hasPending()).length;
  });
  let locked = $derived(loading || submitting || pending > 0);
  function current(v: Variant) {
    void revision;
    const saved = editors.get(v.id)?.snapshot().saved;
    // Refresh may know a newer server version while an editor retains its
    // original version and draft for conflict-safe retry.
    return saved && saved.version >= v.version ? saved : v;
  }
  function chosen(b: Bean) {
    const preferred =
      b.variants.find((v) => v.id === selected[b.id]) ?? b.variants[0];
    return preferred && matchesWeight(current(preferred).weight_g)
      ? preferred
      : b.variants.find((v) => matchesWeight(current(v).weight_g));
  }
  function matchesWeight(weight: number) {
    return (
      (minimumWeight === undefined || weight >= minimumWeight) &&
      (maximumWeight === undefined || weight <= maximumWeight)
    );
  }
  function matchesPurchase(bean: Bean) {
    if (filterError) return false;
    const v = chosen(bean);
    if (!v) return !filtering;
    const quote = cost(current(v), bean.shop, quantity(bean));
    return (
      budget === undefined ||
      (quote.subtotal !== null &&
        (quote.total === null || quote.total <= budget))
    );
  }
  let candidates = $derived.by(() => {
    void revision;
    return beans
      .filter(
        (b) =>
          `${b.name} ${b.shop.name}`
            .toLocaleLowerCase()
            .includes(search.toLocaleLowerCase()) &&
          (statusFilter === 'all' || b.status === statusFilter) &&
          matchesPurchase(b),
      )
      .sort((a, b) => {
        if (sort === 'newest') return b.id - a.id;
        const va = chosen(a),
          vb = chosen(b);
        if (!va) return vb ? 1 : a.id - b.id;
        if (!vb) return -1;
        const av = current(va),
          bv = current(vb);
        return (
          compare(
            {
              price:
                sort === 'unit'
                  ? av.price_yen
                  : cost(av, a.shop, quantity(a)).total,
              weight: av.weight_g,
            },
            {
              price:
                sort === 'unit'
                  ? bv.price_yen
                  : cost(bv, b.shop, quantity(b)).total,
              weight: bv.weight_g,
            },
            sort as 'unit' | 'total',
          ) || a.id - b.id
        );
      });
  });
  // Only explicit list operations apply ordering and purchase filtering.
  // Costs and editors remain live while the current rows stay in place.
  let visibleIds = $derived.by(() => {
    void [
      listVersion,
      search,
      sort,
      statusFilter,
      budget,
      minimumWeight,
      maximumWeight,
    ];
    return untrack(() => candidates.map((bean) => bean.id));
  });
  let displayed = $derived(
    visibleIds.flatMap((id) => {
      const bean = beans.find((b) => b.id === id);
      return bean ? [bean] : [];
    }),
  );
  let includesUnknownShipping = $derived(
    budget !== undefined &&
      displayed.some((bean) => {
        const variant = chosen(bean);
        if (!variant) return false;
        const quote = cost(current(variant), bean.shop, quantity(bean));
        return quote.subtotal !== null && quote.shipping === null;
      }),
  );
  let comparisonChanged = $derived(
    candidates.map((b) => b.id).join(',') !== visibleIds.join(','),
  );
  let orphaned = $derived.by(() => {
    void revision;
    const ids = new Set(beans.flatMap((b) => b.variants.map((v) => v.id)));
    return [...editors.values()].filter(
      (editor) => !ids.has(editor.snapshot().saved.id) && editor.hasPending(),
    );
  });
  function removeEditor(id: number) {
    subscriptions.get(id)?.();
    subscriptions.delete(id);
    editors.delete(id);
    revision++;
  }
  async function discardDeleted(editor: PriceEditor) {
    if (loading || submitting) return;
    if (await load()) {
      editor.discardDeleted();
      const id = editor.snapshot().saved.id;
      if (!beans.some((b) => b.variants.some((v) => v.id === id)))
        removeEditor(id);
      notice = '削除済み容量の下書きを破棄し、一覧を更新しました。';
    }
  }
  function message(e: unknown) {
    if (e instanceof UnauthorizedError) {
      authRequired = true;
      error = '';
      return '';
    }
    return e instanceof Error
      ? e.message
      : '通信に失敗しました。再試行してください。';
  }
  async function signIn(event: SubmitEvent) {
    event.preventDefault();
    if (loginSubmitting) return;
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    loginSubmitting = true;
    loginError = '';
    try {
      await login(
        String(data.get('username') ?? ''),
        String(data.get('password') ?? ''),
      );
      authRequired = false;
      authenticated = true;
      (form.elements.namedItem('password') as HTMLInputElement).value = '';
      await load();
    } catch (e) {
      loginError =
        e instanceof UnauthorizedError
          ? 'ユーザー名またはパスワードが違います。確認して再入力してください。'
          : message(e);
    } finally {
      loginSubmitting = false;
    }
  }
  async function load() {
    loading = true;
    error = '';
    try {
      const [shopData, beanData] = await Promise.all([
        request<{ shops: Shop[] }>('/api/shops'),
        request<{ beans: Bean[] }>('/api/beans'),
      ]);
      shops = shopData.shops;
      const knownBeans = new Map(beans.map((bean) => [bean.id, bean]));
      const fetchedShops = new Map(
        shopData.shops.map((shop) => [shop.id, shop]),
      );
      const nextBeans = beanData.beans.map((fetched) => {
        const known = knownBeans.get(fetched.id);
        const knownVariants = new Map(known?.variants.map((v) => [v.id, v]));
        let version = fetched.version;
        // Keep server membership and metadata, then include saves completed
        // after GET's snapshot. Each variant version step bumps its parent.
        const variants = fetched.variants.map((variant) => {
          const saved = knownVariants.get(variant.id);
          if (!saved || saved.version <= variant.version) return variant;
          version += saved.version - variant.version;
          return saved;
        });
        let shop = fetched.shop;
        for (const candidate of [
          known?.shop,
          fetchedShops.get(fetched.shop_id),
        ])
          if (candidate && candidate.version > shop.version) shop = candidate;
        return { ...fetched, version, variants, shop };
      });
      for (const b of nextBeans)
        for (const v of b.variants) {
          if (editors.has(v.id)) editors.get(v.id)!.reconcile(v);
          else {
            const editor = new PriceEditor(
              v,
              async (version, price) => {
                try {
                  return await request<Variant>(
                    `/api/variants/${v.id}`,
                    'PATCH',
                    {
                      version,
                      price_yen: price,
                    },
                  );
                } catch (e) {
                  if (e instanceof UnauthorizedError) message(e);
                  // Keep the editor's draft and failed-save state for retry
                  // after authentication, including normal conflict handling.
                  throw e;
                }
              },
              () => {
                const saved = editors.get(v.id)!.snapshot().saved;
                beans = beans.map((bean) => {
                  if (bean.id !== b.id) return bean;
                  const known = bean.variants.find(
                    (variant) => variant.id === saved.id,
                  );
                  if (!known) return bean;
                  if (known.version >= saved.version) {
                    if (known.version > saved.version)
                      editors.get(v.id)!.reconcile(known);
                    return bean;
                  }
                  // A refresh may already include this write. Count only
                  // variant updates not yet represented by the bean snapshot.
                  return {
                    ...bean,
                    version: bean.version + saved.version - known.version,
                    variants: bean.variants.map((variant) =>
                      variant.id === saved.id ? saved : variant,
                    ),
                  };
                });
              },
            );
            editors.set(v.id, editor);
            subscriptions.set(
              v.id,
              editor.subscribe(() => revision++),
            );
          }
        }
      const ids = new Set(
        nextBeans.flatMap((b) => b.variants.map((v) => v.id)),
      );
      for (const id of editors.keys())
        if (!ids.has(id)) {
          const editor = editors.get(id)!;
          if (editor.hasPending()) editor.markDeleted();
          else removeEditor(id);
        }
      beans = nextBeans;
      void request<{ authenticated: boolean }>('/auth/session')
        .then((value) => (authenticated = value.authenticated))
        .catch(() => {});
      applyComparison();
      return true;
    } catch (e) {
      error = message(e);
      return false;
    } finally {
      loading = false;
    }
  }
  onMount(() => {
    void load();
    void syncScreen(false);
    const hashChange = () => {
      void syncScreen();
    };
    window.addEventListener('hashchange', hashChange);
    const escape = (event: KeyboardEvent) => {
      if (
        event.key === 'Escape' &&
        screen === 'add' &&
        !submitting &&
        !document.querySelector('dialog[open]')
      )
        navigate('list');
    };
    window.addEventListener('keydown', escape);
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (
        addDirty ||
        shopDirty ||
        pendingBean !== null ||
        [...editors.values()].some((e) => e.hasPending())
      ) {
        event.preventDefault();
        event.returnValue = '';
      }
    };
    window.addEventListener('beforeunload', beforeUnload);
    const focusChange = () => {
      inputFocused = !!document.activeElement?.matches(
        'input, select, textarea, [contenteditable="true"]',
      );
    };
    document.addEventListener('focusin', focusChange);
    document.addEventListener('focusout', focusChange);
    return () => {
      window.removeEventListener('beforeunload', beforeUnload);
      window.removeEventListener('hashchange', hashChange);
      window.removeEventListener('keydown', escape);
      document.removeEventListener('focusin', focusChange);
      document.removeEventListener('focusout', focusChange);
      for (const unsubscribe of subscriptions.values()) unsubscribe();
    };
  });
  function number(
    value: FormDataEntryValue | null,
    required = true,
  ): number | null {
    const text = String(value ?? '');
    if (!required && text === '') return null;
    const n = parsePrice(text);
    if (n === null)
      throw new Error('金額・容量には0〜1,000,000の整数を入力してください。');
    return n;
  }
  async function deleteShop(shop: Shop) {
    if (
      locked ||
      !window.confirm(
        `${shop.name}を削除します。豆が残っている店舗は削除できません。元に戻せません。`,
      )
    )
      return;
    submitting = true;
    error = '';
    try {
      await request(`/api/shops/${shop.id}`, 'DELETE', {
        version: shop.version,
      });
      await load();
    } catch (e) {
      error = message(e);
    } finally {
      submitting = false;
    }
  }
  async function editShop(event: SubmitEvent, shop: Shop) {
    event.preventDefault();
    if (locked) return;
    const data = new FormData(event.currentTarget as HTMLFormElement);
    submitting = true;
    error = '';
    shopNotice = '';
    try {
      const updated = await request<Shop>(`/api/shops/${shop.id}`, 'PATCH', {
        version: shop.version,
        name: data.get('name'),
        shipping_yen: number(data.get('shipping'), false),
        free_shipping_threshold_yen: number(data.get('threshold'), false),
      });
      dirtyShops[shop.id] = false;
      shopNotice = `${updated.name} の店舗情報を保存しました。`;
      shops = shops.map((s) => (s.id === shop.id ? updated : s));
      beans = beans.map((b) =>
        b.shop_id === shop.id ? { ...b, shop: updated } : b,
      );
    } catch (e) {
      error = message(e);
    } finally {
      submitting = false;
    }
  }
  async function addBean(event: SubmitEvent) {
    event.preventDefault();
    if (locked) return;
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    submitting = true;
    error = '';
    try {
      const weight = number(data.get('weight'));
      if (!weight) throw new Error('容量は1g以上にしてください。');
      const price = number(data.get('price'));
      if (pendingBean === null) {
        if (!chosenShop) {
          const name = shopName.trim();
          if (!name) throw new Error('店舗名を入力してください。');
          const existing = shops.find(
            (s) => normalizeShop(s.name) === normalizeShop(name),
          );
          if (!existing && similarShops.length && !allowSimilar)
            throw new Error(
              '似た名前の店舗があります。既存店舗を選ぶか、新しい店舗として登録する確認をしてください。',
            );
          const shop =
            existing ?? (await request<Shop>('/api/shops', 'POST', { name }));
          chosenShop = String(shop.id);
          if (!existing) shops = [...shops, shop];
        }
        const bean = await request<Bean>('/api/beans', 'POST', {
          name: data.get('name'),
          shop_id: Number(chosenShop),
          product_url: data.get('url') || null,
        });
        pendingBean = bean.id;
      }
      registrationConflict = null;
      // A failed POST response may have followed a committed write. Inspect
      // the original attempted package before considering another POST.
      if (initialAttempt) {
        const result = await request<{ beans: Bean[] }>('/api/beans');
        const bean = result.beans.find((b) => b.id === pendingBean);
        if (!bean)
          throw new Error(
            '登録途中の豆は削除されています。「登録を中断して一覧へ」から戻れます。',
          );
        const existing = bean.variants.find(
          (v) => v.weight_g === initialAttempt!.weight_g,
        );
        if (existing) {
          if (existing.weight_g !== weight || existing.price_yen !== price) {
            registrationConflict = existing;
            error =
              '保存済みの容量が入力と異なります。内容を確認してください。';
            return;
          }
          await finishRegistration(form);
          return;
        }
      }
      initialAttempt = { weight_g: weight, price_yen: price! };
      await request(`/api/beans/${pendingBean}/variants`, 'POST', {
        weight_g: weight,
        price_yen: price,
      });
      await finishRegistration(form);
    } catch (e) {
      error =
        pendingBean === null
          ? message(e)
          : `豆は登録済みです。容量の登録を再試行してください。${message(e)}`;
    } finally {
      submitting = false;
    }
  }
  async function finishRegistration(form: HTMLFormElement) {
    pendingBean = null;
    initialAttempt = null;
    registrationConflict = null;
    form.reset();
    chosenShop = '';
    shopName = '';
    addDirty = false;
    await load();
    navigate('list');
  }
  async function acceptRegistered() {
    if (locked || !registrationConflict) return;
    submitting = true;
    try {
      // Recheck before accepting a value that another client can change.
      const result = await request<{ beans: Bean[] }>('/api/beans');
      const existing = result.beans
        .find((b) => b.id === pendingBean)
        ?.variants.find((v) => v.id === registrationConflict!.id);
      if (!existing)
        throw new Error(
          '保存済みの容量が見つかりません。登録結果を再確認してください。',
        );
      if (existing.version !== registrationConflict.version) {
        registrationConflict = existing;
        error = '保存済みの容量が変更されました。もう一度確認してください。';
        return;
      }
      await finishRegistration(
        document.getElementById('registration-form') as HTMLFormElement,
      );
    } catch (e) {
      error = message(e);
    } finally {
      submitting = false;
    }
  }
  async function stopRegistration() {
    if (locked) return;
    // The explicit action abandons the workflow, never server-side records.
    await finishRegistration(
      document.getElementById('registration-form') as HTMLFormElement,
    );
    notice = '登録を中断しました。保存済みの豆・容量は一覧で確認してください。';
  }
  async function addVariant(event: SubmitEvent, bean: Bean) {
    event.preventDefault();
    if (locked) return;
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    submitting = true;
    error = '';
    try {
      const weight = number(data.get('weight'));
      if (!weight) throw new Error('容量は1g以上にしてください。');
      await request(`/api/beans/${bean.id}/variants`, 'POST', {
        weight_g: weight,
        price_yen: number(data.get('price')),
      });
      form.reset();
      await load();
    } catch (e) {
      error = message(e);
    } finally {
      submitting = false;
    }
  }
  async function changeBean(
    bean: Bean,
    change: Partial<Pick<Bean, 'status' | 'buy_again'>>,
  ) {
    if (locked) return;
    submitting = true;
    error = '';
    try {
      const updated = await request<Bean>(`/api/beans/${bean.id}`, 'PATCH', {
        version: bean.version,
        ...change,
      });
      beans = beans.map((b) =>
        b.id === bean.id
          ? {
              ...b,
              status: updated.status,
              buy_again: updated.buy_again,
              version: updated.version,
            }
          : b,
      );
      if (change.status) applyComparison();
      if (
        change.status &&
        statusFilter !== 'all' &&
        updated.status !== statusFilter
      ) {
        notice = `${bean.name}を${updated.status === 'excluded' ? '候補から外しました' : '候補に戻しました'}。`;
        document.getElementById('comparison-results')?.focus();
      }
    } catch (e) {
      error = message(e);
    } finally {
      submitting = false;
    }
  }
  function showPending() {
    navigate('list');
    search = '';
    statusFilter = 'all';
    clearFilters();
    for (const b of beans) expanded[b.id] = true;
  }
</script>

<svelte:window bind:scrollY />
<svelte:head
  ><meta
    name="description"
    content="コーヒー豆を100g単価と送料込みの購入総額で比較。容量別の価格を一覧から編集できます。"
  /></svelte:head
>
<div class="shell">
  <header class="topbar" id="page-top" tabindex="-1">
    <a class="brand" href="#list" aria-label="豆くらべ ホーム">豆くらべ</a>
    <h1>{copy.title}</h1>
    <nav aria-label="メインナビゲーション" hidden={authRequired}>
      <a href="#list" aria-current={screen !== 'shops' ? 'page' : undefined}
        >比較一覧</a
      >
      <a href="#shops" aria-current={screen === 'shops' ? 'page' : undefined}
        >店舗設定</a
      >
    </nav>
    <button
      id="add-trigger"
      class="primary"
      hidden={authRequired}
      onclick={() => navigate('add')}
      disabled={loading || submitting}>＋ 豆を追加</button
    >
  </header>
  <main>
    {#if authRequired}
      <section class="login-panel" aria-labelledby="login-title">
        <h2 id="login-title">ログイン</h2>
        <p class="login-description">
          ユーザー名とパスワードを入力してください。
        </p>
        <form
          class="login-form"
          onsubmit={signIn}
          oninput={() => (loginError = '')}
        >
          <label
            >ユーザー名<input
              name="username"
              autocomplete="username"
              required
              disabled={loginSubmitting}
            /></label
          >
          <label
            >パスワード<input
              name="password"
              type="password"
              autocomplete="current-password"
              required
              disabled={loginSubmitting}
            /></label
          >
          {#if loginError}<p class="alert" role="alert">{loginError}</p>{/if}
          <button class="primary" disabled={loginSubmitting}
            >{loginSubmitting ? '確認しています…' : 'ログイン'}</button
          >
        </form>
      </section>
    {/if}
    {#if error}<div class="alert" role="alert">{error}</div>{/if}
    <div
      class="workspace"
      class:adding={showAdd}
      hidden={authRequired || screen === 'shops'}
    >
      <section
        class="registration add-panel"
        aria-label="豆の登録"
        hidden={!showAdd}
      >
        <div class="panel-heading">
          <h2 id="add-title" tabindex="-1">{copy.addTitle}</h2>
          <button
            class="secondary"
            onclick={() => navigate('list')}
            disabled={submitting}>閉じる</button
          >
        </div>
        <form
          id="registration-form"
          class="form-grid"
          onsubmit={addBean}
          oninput={() => (addDirty = true)}
        >
          <fieldset disabled={submitting}>
            <label class="wide"
              >豆の名前<input
                id="bean-name"
                name="name"
                required
                maxlength="200"
                disabled={pendingBean !== null}
              /></label
            >
            <ShopPicker
              {shops}
              value={chosenShop}
              disabled={submitting || pendingBean !== null}
              onchange={(id) => {
                chosenShop = id;
                addDirty = true;
                allowSimilar = false;
              }}
            />
            <label class="wide" hidden={!!chosenShop}
              >店舗名
              <input
                name="shop"
                aria-label="店舗名"
                aria-describedby="shop-help"
                bind:value={shopName}
                oninput={() => (allowSimilar = false)}
                required={!chosenShop}
                maxlength="200"
                disabled={pendingBean !== null || !!chosenShop}
                placeholder="新しい店舗名"
              />
            </label>
            {#if !chosenShop && similarShops.length}
              <div class="wide similar-shops">
                <p>似た名前の登録済み店舗があります。</p>
                {#each similarShops.slice(0, 5) as shop}<button
                    type="button"
                    class="secondary"
                    onclick={() => (chosenShop = String(shop.id))}
                    >{shop.name}を使う</button
                  >{/each}
                <label
                  ><input
                    type="checkbox"
                    bind:checked={allowSimilar}
                  />別の店舗として新しく登録する</label
                >
              </div>
            {/if}
            <span id="shop-help" class="wide">
              {#if selectedShop}
                登録済みの店舗名・送料・送料無料条件を使います。
              {:else}
                登録済みの店舗は一覧から選べます。新しい店舗は豆と一緒に登録し、送料は後から設定できます。
              {/if}
            </span>
            <label
              >容量（g）<input
                name="weight"
                type="number"
                min="1"
                max="1000000"
                step="1"
                required
              /></label
            >
            <label
              >価格（円）<input
                name="price"
                type="number"
                min="0"
                max="1000000"
                step="1"
                required
              /></label
            >
            <label class="wide"
              >商品URL（任意）<input
                name="url"
                type="url"
                maxlength="4096"
                placeholder="https://…"
                disabled={pendingBean !== null}
              /></label
            >
            <button class="primary" disabled={locked}
              >{pendingBean === null
                ? '豆を登録'
                : '容量の登録を再試行'}</button
            >
            {#if pendingBean !== null}
              <button
                type="button"
                class="secondary"
                disabled={locked}
                onclick={stopRegistration}>登録を中断して一覧へ</button
              >
            {/if}
          </fieldset>
          {#if registrationConflict}
            <div class="alert wide" role="status">
              保存済み: {registrationConflict.weight_g}g・{yen(
                registrationConflict.price_yen,
              )}。入力内容は保持しています。
              <button
                type="button"
                class="secondary"
                disabled={locked}
                onclick={acceptRegistered}>保存済みの容量で登録を完了</button
              >
            </div>
          {/if}
        </form>
      </section>
      <section class="collection" aria-label="豆の比較一覧">
        <div class="collection-heading">
          <h2>比較一覧 <span>{displayed.length} / {beans.length}件</span></h2>
          {#if pending}<button class="text-button" onclick={showPending}
              >未保存 {pending}件・編集を確認</button
            >{/if}
        </div>
        <section class="comparison-controls" aria-label="比較条件">
          <p class="condition-summary" aria-live="polite">
            表示中：{appliedTarget === undefined
              ? '容量・個数を個別指定'
              : `${appliedTarget}g以内`} ／ {sort === 'unit'
              ? '100g単価順'
              : sort === 'total'
                ? '購入総額順'
                : '登録が新しい順'} ／ {statusFilter === 'candidate'
              ? '候補'
              : statusFilter === 'excluded'
                ? '候補外'
                : 'すべて'}{search ? ` ／ 検索「${search}」` : ''}{budget !==
            undefined
              ? ` ／ 予算${budget}円以内`
              : ''}{minimumWeight !== undefined
              ? ` ／ 1袋${minimumWeight}g以上`
              : ''}{maximumWeight !== undefined
              ? ` ／ 1袋${maximumWeight}g以下`
              : ''}
          </p>
          <div class="condition-actions">
            <button
              type="button"
              class="secondary"
              aria-expanded={conditionsOpen}
              aria-controls="comparison-conditions"
              onclick={() => (conditionsOpen = !conditionsOpen)}
              ><span aria-hidden="true">{conditionsOpen ? '▾' : '▸'}</span>
              {conditionsOpen ? '条件を閉じる' : '条件を変更'}</button
            >
            {#if conditionsDirty}<span class="condition-pending" role="status"
                >未適用</span
              >{/if}
          </div>
          <form
            id="comparison-conditions"
            class="form-grid"
            hidden={!conditionsOpen}
            oninput={(event) => {
              conditionsInvalid =
                !!event.currentTarget.querySelector(':invalid');
            }}
            onsubmit={compareNow}
          >
            <fieldset disabled={loading || submitting || pendingBean !== null}>
              <label
                >購入量の目安（g以内）<input
                  aria-label="希望購入重量（g）"
                  type="number"
                  inputmode="numeric"
                  min="1"
                  max={maxTargetWeight}
                  bind:value={
                    () => draft.target ?? null,
                    (value: number | null | undefined) =>
                      (draft.target = value ?? undefined)
                  }
                /><span>空欄は容量・個数を個別に指定</span></label
              >
              <label
                >並び順<select aria-label="並び順" bind:value={draft.sort}
                  ><option value="unit">100g単価が安い順</option><option
                    value="total">購入総額が安い順</option
                  ><option value="newest">登録が新しい順</option></select
                ></label
              >
              <details class="wide">
                <summary>絞り込み条件</summary>
                <div class="condition-fields">
                  <label
                    >豆・店舗名で検索<input
                      aria-label="豆・店舗名で検索"
                      bind:value={draft.search}
                    /></label
                  >
                  <label
                    >状態<select
                      aria-label="状態で絞り込み"
                      bind:value={draft.status}
                      ><option value="candidate">候補</option><option
                        value="excluded">候補外</option
                      ><option value="all">すべて</option></select
                    ></label
                  >
                  <label
                    >予算上限（送料込・円）<input
                      name="budget"
                      type="number"
                      inputmode="numeric"
                      min="0"
                      bind:value={
                        () => draft.budget ?? null,
                        (value: number | null | undefined) =>
                          (draft.budget = value ?? undefined)
                      }
                    /></label
                  >
                  <label
                    >1袋の容量下限（g）<input
                      name="minimumWeight"
                      aria-label="容量の下限（g）"
                      type="number"
                      inputmode="numeric"
                      min="0"
                      bind:value={
                        () => draft.min ?? null,
                        (value: number | null | undefined) =>
                          (draft.min = value ?? undefined)
                      }
                    /></label
                  >
                  <label
                    >1袋の容量上限（g）<input
                      name="maximumWeight"
                      aria-label="容量の上限（g）"
                      type="number"
                      inputmode="numeric"
                      min="0"
                      bind:value={
                        () => draft.max ?? null,
                        (value: number | null | undefined) =>
                          (draft.max = value ?? undefined)
                      }
                    /></label
                  >
                  <button
                    type="button"
                    class="secondary"
                    onclick={async (event) => {
                      const form = event.currentTarget.form!;
                      draft.budget = draft.min = draft.max = undefined;
                      // Number bindings cannot represent an unfinished value like "1e".
                      for (const name of [
                        'budget',
                        'minimumWeight',
                        'maximumWeight',
                      ])
                        (
                          form.elements.namedItem(name) as HTMLInputElement
                        ).value = '';
                      await tick();
                      conditionsInvalid = !!form.querySelector(':invalid');
                    }}>予算・容量をクリア</button
                  >
                </div>
              </details>
              <p class="wide">
                購入量・1袋の容量条件を変更すると、容量と個数を選び直します。希望量以内の大袋を優先し、同じ容量の袋だけで試算します。
              </p>
              <button class="primary">この条件で比較</button>
            </fieldset>
          </form>
          {#if comparisonChanged}<p class="notice" role="status">
              金額が変わりました。「条件を変更」から再適用すると行順・予算条件を反映します。
            </p>{/if}
          <p class="notice" role="status">{notice}</p>
          {#if includesUnknownShipping}<p class="filter-hint">
              送料未設定も含む
            </p>{/if}
        </section>
        <span id="comparison-results" tabindex="-1"></span>
        {#if orphaned.length}
          <section
            class="orphaned-drafts"
            aria-label="一覧から削除された容量の下書き"
          >
            <h3>一覧から削除された容量の下書き</h3>
            {#each orphaned as editor (editor.snapshot().saved.id)}
              <PriceInput
                {editor}
                label={`削除済み容量 ${editor.snapshot().saved.id} の価格`}
                onDiscardDeleted={() => discardDeleted(editor)}
              />
            {/each}
          </section>
        {/if}
        <div class="list-head" aria-hidden="true">
          <span>豆・焙煎店</span><span>容量</span><span>価格</span><span
            >商品100g単価</span
          ><span>個数・購入総額</span>
        </div>
        {#if loading && beans.length === 0}<div class="empty" role="status">
            比較リストを読み込んでいます…
          </div>
        {:else if beans.length === 0}<div class="empty">
            <h3>{copy.emptyTitle}</h3>
            <p>{copy.emptyDescription}</p>
            <button class="primary" onclick={() => navigate('add')}
              >最初の豆を登録</button
            >
          </div>
        {:else if displayed.length === 0}<div class="empty">
            条件に合う豆がありません。検索・状態・予算・容量を変更してください。
          </div>
        {:else}
          {#each displayed as bean (bean.id)}
            {@const variant = chosen(bean)}
            {@const saved = variant ? current(variant) : null}
            {@const amounts = saved
              ? cost(saved, bean.shop, quantity(bean))
              : null}
            <article class="bean-card" aria-label={bean.name}>
              <div class="bean-row">
                <div class="bean-info">
                  <div>
                    <span class="shop-name">{bean.shop.name}</span>
                    <h3>
                      {#if bean.product_url}<a
                          href={bean.product_url}
                          target="_blank"
                          rel="noreferrer"
                          >{bean.name}
                          <span class="external" aria-hidden="true">↗</span></a
                        >{:else}{bean.name}{/if}
                    </h3>
                    <button
                      class="buy-again"
                      class:liked={bean.buy_again}
                      aria-label={`${bean.name} ${bean.buy_again ? 'また買いたいを解除' : 'また買いたい'}`}
                      aria-pressed={bean.buy_again}
                      title={bean.buy_again
                        ? 'また買いたいを解除'
                        : 'また買いたい'}
                      disabled={locked}
                      onclick={() =>
                        changeBean(bean, { buy_again: !bean.buy_again })}
                    >
                      <svg
                        aria-hidden="true"
                        width="18"
                        height="18"
                        viewBox="0 0 24 24"
                        fill={bean.buy_again ? 'currentColor' : 'none'}
                        stroke="currentColor"
                        stroke-width="1.8"
                        stroke-linecap="round"
                        stroke-linejoin="round"
                      >
                        <path
                          d="M20.8 4.6a5.5 5.5 0 0 0-7.8 0L12 5.7l-1.1-1.1a5.5 5.5 0 0 0-7.8 7.8L12 21l8.8-8.6a5.5 5.5 0 0 0 0-7.8Z"
                        />
                      </svg>
                    </button>
                    <select
                      class="status-select"
                      aria-label={`${bean.name} の状態`}
                      value={bean.status}
                      disabled={locked}
                      onchange={(e) => {
                        const requested = e.currentTarget.value;
                        e.currentTarget.value = bean.status;
                        void changeBean(bean, {
                          status: requested as Bean['status'],
                        });
                      }}
                      ><option value="candidate">候補</option><option
                        value="excluded">候補外</option
                      ></select
                    >
                  </div>
                </div>
                <div class="size-cell">
                  <span class="mobile-label">容量</span>{#if variant}<select
                      aria-label={`${bean.name} の容量`}
                      value={variant.id}
                      onchange={(e) =>
                        (selected[bean.id] = Number(e.currentTarget.value))}
                      >{#each bean.variants.filter( (v) => matchesWeight(current(v).weight_g) ) as v}<option
                          value={v.id}>{current(v).weight_g}g</option
                        >{/each}</select
                    >{:else}<span class="muted">容量未登録</span>{/if}
                </div>
                <div class="price-cell">
                  <span class="mobile-label">価格</span>{#if variant}<PriceInput
                      editor={editors.get(variant.id)!}
                      label={`${bean.name} ${saved?.weight_g ?? variant.weight_g}g の価格`}
                      onDiscardDeleted={() =>
                        discardDeleted(editors.get(variant.id)!)}
                    />{:else}—{/if}
                </div>
                <div class="metric">
                  <span class="mobile-label">商品100g単価</span><strong
                    >{amounts ? yen(amounts.unit) : '—'}</strong
                  ><small>/ 100g</small>
                </div>
                <div class="metric total">
                  <span class="mobile-label">個数・購入総額</span>
                  <div class="purchase-main">
                    {#if variant}
                      <label class="quantity-input"
                        ><span class="sr-only">{bean.name} の購入個数</span
                        ><input
                          type="number"
                          min="1"
                          max={maxQuantity}
                          step="1"
                          inputmode="numeric"
                          value={quantityValue(bean)}
                          aria-invalid={quantityInvalid(bean)}
                          aria-describedby={quantityError(bean)
                            ? `quantity-error-${bean.id}`
                            : undefined}
                          oninput={(e) => {
                            quantityDrafts[bean.id] = e.currentTarget.value;
                          }}
                          onblur={() => commitQuantity(bean.id)}
                          onkeydown={(e) => {
                            if (e.key === 'Enter' && !e.isComposing) {
                              e.preventDefault();
                              e.currentTarget.blur();
                            }
                          }}
                        />個</label
                      >
                    {/if}
                    <strong
                      >{amounts?.total !== null && amounts?.total !== undefined
                        ? yen(amounts.total)
                        : '—'}</strong
                    >
                  </div>
                  {#if !amounts}<small>容量を登録してください</small>
                  {:else if amounts.subtotal !== null}<small
                      >{`${amounts.weight?.toLocaleString('ja-JP')}g・${amounts.shipping === null ? `商品 ${yen(amounts.subtotal)}・送料未設定` : amounts.shipping === 0 ? '送料無料' : `送料 ${yen(amounts.shipping)} 込み`}`}</small
                    >{/if}
                  {#if quantityError(bean)}<small
                      id={`quantity-error-${bean.id}`}
                      class="quantity-error"
                      aria-live="polite">{quantityError(bean)}</small
                    >{/if}
                  {#if weightTrial}<small class="weight-result"
                      >{weightMessage(bean, saved)}</small
                    >{/if}
                </div>
              </div>
              <div class="row-footer">
                <button
                  class="text-button"
                  aria-expanded={!!expanded[bean.id]}
                  onclick={() => (expanded[bean.id] = !expanded[bean.id])}
                  >{expanded[bean.id]
                    ? '▴ 容量を閉じる'
                    : `▾ すべての容量（${bean.variants.length}）`}</button
                >
              </div>
              <details
                class="management-details"
                ontoggle={(e) => {
                  if (e.currentTarget.open) managing[bean.id] = true;
                }}
              >
                <summary>編集・削除</summary>{#if managing[bean.id]}<BeanManager
                    {bean}
                    disabled={locked}
                    onchanged={load}
                    onfailure={(e) => {
                      if (e instanceof UnauthorizedError) message(e);
                    }}
                  />{/if}
              </details>
              {#if expanded[bean.id]}<div class="variant-panel">
                  <h4>容量ごとの価格</h4>
                  {#if amounts?.effective !== null && amounts?.effective !== undefined}<p
                      class="variant-note"
                    >
                      送料込み {yen(amounts.effective)} / 100g
                    </p>{/if}
                  <div class="variant-grid">
                    {#each bean.variants as v (v.id)}<div class="variant-item">
                        <span>{current(v).weight_g}g</span><PriceInput
                          editor={editors.get(v.id)!}
                          label={`${bean.name} ${current(v).weight_g}g の価格（展開）`}
                          onDiscardDeleted={() =>
                            discardDeleted(editors.get(v.id)!)}
                        />
                      </div>{/each}
                  </div>
                  <form
                    class="variant-form"
                    onsubmit={(event) => addVariant(event, bean)}
                  >
                    <label
                      >追加する容量（g）<input
                        name="weight"
                        type="number"
                        min="1"
                        max="1000000"
                        step="1"
                        required
                      /></label
                    ><label
                      >価格（円）<input
                        name="price"
                        type="number"
                        min="0"
                        max="1000000"
                        step="1"
                        required
                      /></label
                    ><button class="secondary" disabled={locked}
                      >容量を追加</button
                    >
                  </form>
                </div>{/if}
            </article>
          {/each}
        {/if}
        <details class="list-note">
          <summary>価格の保存と計算について</summary>
          <p>
            {copy.description} 価格はEnterまたはフォーカス移動で自動保存します。単価・総額は保存済み価格から算出。比較条件は再読込で初期状態に戻り、個数は1個に戻ります。送料は商品価格×個数で無料条件を判定し、1回分を加算します。送料未設定の場合、無料条件を満たす場合を除き総額は表示しません。円未満は四捨五入します。
          </p>
        </details>
      </section>
    </div>
    <section
      class="shop-settings"
      aria-label="店舗情報"
      hidden={authRequired || screen !== 'shops'}
    >
      <div class="settings-heading">
        <h2 id="shops-title" tabindex="-1">店舗設定</h2>
        <a href="#list">比較一覧へ戻る</a>
      </div>
      <p class="settings-description">
        送料・送料無料条件は、同じ店舗のすべての豆に適用します。空欄は未設定、0円は無料です。
      </p>
      <button
        class="refresh"
        disabled={locked || pendingBean !== null}
        onclick={load}>一覧を更新</button
      >
      <p class="notice" role="status">{shopNotice}</p>
      {#if authenticated}<button
          type="button"
          class="secondary"
          disabled={locked || addDirty || shopDirty}
          onclick={signOut}>この端末からログアウト</button
        >
        <p class="filter-hint">
          ログインは30日間有効です。未保存の入力を保存してからログアウトしてください。
        </p>{/if}
      {#each newestShops as shop (shop.id)}
        <form
          class="form-grid"
          aria-label={`${shop.name} の店舗情報`}
          onsubmit={(e) => editShop(e, shop)}
          oninput={() => {
            dirtyShops[shop.id] = true;
            shopNotice = '';
          }}
        >
          <fieldset disabled={submitting}>
            <label
              >店舗名<input
                name="name"
                value={shop.name}
                required
                maxlength="200"
              /></label
            >
            <label
              >送料（円）<input
                name="shipping"
                type="number"
                min="0"
                max="1000000"
                step="1"
                value={shop.shipping_yen ?? ''}
                placeholder="空欄は未設定"
              /></label
            >
            <label
              >送料無料（円）<input
                name="threshold"
                type="number"
                min="0"
                max="1000000"
                step="1"
                value={shop.free_shipping_threshold_yen ?? ''}
                placeholder="条件なしなら空欄"
              /></label
            >
            <button class="secondary" disabled={locked}>店舗情報を保存</button>
            <button
              type="button"
              class="secondary danger"
              disabled={locked}
              onclick={() => deleteShop(shop)}>店舗を削除</button
            >
          </fieldset>
        </form>
      {:else}<p>豆を追加すると、ここで店舗情報を編集できます。</p>{/each}
    </section>
    <footer class="page-footer" hidden={authRequired}>
      {#if copy.footer}<span>{copy.footer}</span>{/if}
    </footer>
  </main>
  {#if scrollY > 400 && !authRequired && !editingVisible}
    <button
      type="button"
      class="back-to-top secondary"
      aria-label="ページ上部へ戻る"
      onclick={backToTop}>↑ ページ上部へ戻る</button
    >
  {/if}
</div>
