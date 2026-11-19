import { ConflictError, NotFoundError } from './autosave.ts';
export { NotFoundError } from './autosave.ts';
import type { Variant } from './types.ts';

export class UnauthorizedError extends Error {
  constructor() {
    super('ログインが必要です。');
    this.name = 'UnauthorizedError';
  }
}

export async function login(username: string, password: string): Promise<void> {
  let authorization: string;
  try {
    authorization = `Basic ${btoa(String.fromCharCode(...new TextEncoder().encode(`${username}:${password}`)))}`;
  } catch {
    throw new Error('ユーザー名またはパスワードを確認してください。');
  }
  const response = await fetch('/auth/login', {
    method: 'POST',
    headers: { Authorization: authorization, Accept: 'application/json' },
    signal: AbortSignal.timeout(15_000),
  });
  if (response.status === 401) throw new UnauthorizedError();
  if (!response.ok)
    throw new Error('ログインできませんでした。再試行してください。');
}

export async function request<T>(
  path: string,
  method = 'GET',
  body?: unknown,
): Promise<T> {
  const response = await fetch(path, {
    method,
    headers:
      body === undefined
        ? { Accept: 'application/json' }
        : {
            Accept: 'application/json',
            'Content-Type': 'application/json',
          },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(15_000),
  });
  if (!response.ok) {
    if (response.status === 401) throw new UnauthorizedError();
    if (response.status === 404) throw new NotFoundError();
    const data = await response.json().catch(() => ({}));
    if (response.status === 409 && data.current && 'price_yen' in data.current)
      throw new ConflictError(data.current as Variant);
    if (data.error === 'shop still contains beans')
      throw new Error(
        'この店舗には豆が残っています。先に豆を削除してください。',
      );
    throw new Error(
      response.status === 409
        ? '他の変更があります。一覧を更新して確認してください。'
        : (data.error ?? '通信に失敗しました。'),
    );
  }
  return (await response.json()) as T;
}

export async function logout(): Promise<void> {
  const response = await fetch('/auth/logout', {
    method: 'POST',
    headers: { Accept: 'application/json' },
    signal: AbortSignal.timeout(15000),
  });
  if (!response.ok)
    throw new Error('ログアウトできませんでした。再試行してください。');
}
