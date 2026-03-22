
export type AccountType =
    | 'ROOT' | 'ASSET' | 'BANK' | 'CASH'
    | 'LIABILITY' | 'CREDIT'
    | 'INCOME' | 'EXPENSE' | 'EQUITY';

// Types the user can pick when creating/editing accounts
export const USER_ACCOUNT_TYPES: AccountType[] = [
    'ASSET', 'BANK', 'CASH', 'CREDIT', 'LIABILITY',
];

export interface Account {
    guid: string;
    name: string;
    account_type: AccountType;
    parent_guid: string | null;
    placeholder: boolean;
    description: string;
    metadata: Record<string, unknown>;
    version: number;
    balance: number;
    reconciled_balance: number;
    aggregated_balance?: number;
    aggregated_reconciled_balance?: number;
    last_reconciled?: string;
    children?: Account[];
}

export interface Split {
    guid: string;
    tx_guid: string;
    account_guid: string;
    memo: string;
    value_num: number;
    value_denom: number;
    quantity_num: number;
    quantity_denom: number;
    reconcile_state: string;
    account_name?: string;
}

export interface Transaction {
    guid: string;
    custom_id: string;
    post_date: string;
    enter_date: string;
    description: string;
    metadata: Record<string, unknown>;
    version: number;
    splits: Split[];
}

export interface RegisterEntry {
    transaction_guid: string;
    custom_id: string;
    post_date: string;
    description: string;
    transfer_account: string;
    transfer_account_guid: string;
    deposit: number | null;
    withdrawal: number | null;
    balance: number;
    split_memo: string;
    split_guid: string;
    reconcile_state: string;
}

export interface RegisterPage {
    entries: RegisterEntry[];
    has_before: boolean;
    has_after: boolean;
    first_offset: number;
    last_offset: number;
    total_count: number;
}

export interface CreateTransactionRequest {
    custom_id: string;
    post_date: string;
    description: string;
    splits: {
        account_guid: string;
        memo: string;
        value_num: number;
        value_denom: number;
        quantity_num: number;
        quantity_denom: number;
    }[];
}

export const ACCOUNT_TYPE_LABELS: Record<AccountType, string> = {
    ROOT: 'Root',
    ASSET: 'Asset',
    BANK: 'Bank',
    CASH: 'Cash',
    LIABILITY: 'Liability',
    CREDIT: 'Credit Card',
    INCOME: 'Income',
    EXPENSE: 'Expense',
    EQUITY: 'Equity',
};

export const ACCOUNT_TYPE_COLORS: Record<AccountType, string> = {
    ROOT: '#64748b',
    ASSET: '#22c55e',
    BANK: '#3b82f6',
    CASH: '#10b981',
    LIABILITY: '#ef4444',
    CREDIT: '#f97316',
    INCOME: '#8b5cf6',
    EXPENSE: '#ec4899',
    EQUITY: '#06b6d4',
};
