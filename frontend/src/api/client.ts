import type { Account, Transaction, RegisterEntry, RegisterPage, CreateTransactionRequest, SnapshotConfig, SnapshotSummary } from '../types';

const API_BASE = '/api';

function getToken(): string | null {
    return localStorage.getItem('antimoney-token');
}

async function fetchJSON<T>(url: string, options?: RequestInit): Promise<T> {
    const token = getToken();
    const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        ...(options?.headers as Record<string, string> || {}),
    };
    if (token) {
        headers['Authorization'] = `Bearer ${token}`;
    }

    const res = await fetch(`${API_BASE}${url}`, {
        ...options,
        headers,
    });

    if (res.status === 401) {
        // Token expired or invalid — force logout
        localStorage.removeItem('antimoney-token');
        window.location.reload();
        throw new Error('Session expired');
    }

    if (!res.ok) {
        const body = await res.json().catch(() => ({ error: res.statusText }));
        throw new Error(body.error || `HTTP ${res.status}`);
    }

    if (res.status === 204) return undefined as T;
    return res.json();
}

// Accounts
export const getAccounts = (start?: string, end?: string) => {
    const query = new URLSearchParams();
    if (start) query.append('start', start);
    if (end) query.append('end', end);
    const qs = query.toString();
    return fetchJSON<Account[]>(`/accounts${qs ? '?' + qs : ''}`);
};
export const getAccount = (id: string) => fetchJSON<Account>(`/accounts/${id}`);
export const getAccountRegister = (id: string) => fetchJSON<RegisterEntry[]>(`/accounts/${id}/register`);
export const getAccountRegisterPaged = (
    id: string,
    cursorDate: string,
    direction: 'before' | 'after' | 'around',
    limit = 50,
) => {
    const params = new URLSearchParams({ cursor_date: cursorDate, direction, limit: String(limit) });
    return fetchJSON<RegisterPage>(`/accounts/${id}/register?${params}`);
};

export const createAccount = (data: {
    name: string;
    account_type: string;
    parent_guid: string | null;
    placeholder: boolean;
    description: string;
}) => fetchJSON<Account>('/accounts', { method: 'POST', body: JSON.stringify(data) });

export const updateAccount = (id: string, data: {
    name?: string;
    description?: string;
    placeholder?: boolean;
    account_type?: string;
    parent_guid?: string | null;
    version: number;
}) => fetchJSON<Account>(`/accounts/${id}`, { method: 'PUT', body: JSON.stringify(data) });

export const deleteAccount = (id: string) =>
    fetchJSON<void>(`/accounts/${id}`, { method: 'DELETE' });

// Transactions
export const getTransactions = (limit = 50, offset = 0) =>
    fetchJSON<Transaction[]>(`/transactions?limit=${limit}&offset=${offset}`);

export const getTransaction = (id: string) => fetchJSON<Transaction>(`/transactions/${id}`);

export const createTransaction = (data: CreateTransactionRequest) =>
    fetchJSON<Transaction>('/transactions', {
        method: 'POST',
        body: JSON.stringify(data),
    });

export const updateTransaction = (id: string, data: CreateTransactionRequest) =>
    fetchJSON<Transaction>(`/transactions/${id}`, {
        method: 'PUT',
        body: JSON.stringify(data),
    });

export const deleteTransaction = (id: string) =>
    fetchJSON<void>(`/transactions/${id}`, { method: 'DELETE' });

export const batchReconcileSplits = (splitGuids: string[]) =>
    fetchJSON<void>('/transactions/splits/reconcile', {
        method: 'POST',
        body: JSON.stringify({ split_guids: splitGuids }),
    });

export const toggleSplitAcknowledge = (splitId: string, state: string) =>
    fetchJSON<void>(`/transactions/splits/${splitId}/toggle`, {
        method: 'PATCH',
        body: JSON.stringify({ state }),
    });

export const getReconciledBalance = (accountId: string) =>
    fetchJSON<{ balance: number }>(`/accounts/${accountId}/reconciled-balance`);

export const reconcileAccountSplits = (accountId: string, accountGuids: string[]) =>
    fetchJSON<{ reconciled: number }>(`/accounts/${accountId}/reconcile`, {
        method: 'POST',
        body: JSON.stringify({ account_guids: accountGuids }),
    });

// Snapshots
export const getSnapshotConfig = () =>
    fetchJSON<SnapshotConfig>('/snapshots/config');

export const upsertSnapshotConfig = (data: { frequency_hours: number; ttl_hours: number; active_mode: boolean }) =>
    fetchJSON<SnapshotConfig>('/snapshots/config', {
        method: 'PUT',
        body: JSON.stringify(data),
    });

export const listSnapshots = () =>
    fetchJSON<SnapshotSummary[]>('/snapshots/');

export const takeSnapshot = (label: string) =>
    fetchJSON<SnapshotSummary>('/snapshots/', {
        method: 'POST',
        body: JSON.stringify({ label }),
    });

export const restoreSnapshot = (id: string) =>
    fetchJSON<{ message: string }>(`/snapshots/${id}/restore`, { method: 'POST' });

export const deleteSnapshot = (id: string) =>
    fetchJSON<void>(`/snapshots/${id}`, { method: 'DELETE' });

