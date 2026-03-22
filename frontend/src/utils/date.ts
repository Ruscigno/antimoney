import { KeyboardEvent } from 'react';

/**
 * Handles keyboard shortcuts for date input fields.
 * + / =: increase by 1 day
 * -: decrease by 1 day
 * Shift + / =: increase by 7 days
 * Shift -: decrease by 7 days
 */
export function handleDateShortcut(e: KeyboardEvent<HTMLInputElement>, value: string, setValue: (val: string) => void) {
    if (e.key === '+' || e.key === '=' || e.key === '-') {
        e.preventDefault();
        // Parse date carefully to avoid timezone shifts changing the day
        const dateStr = value ? value : new Date().toISOString().slice(0, 10);
        const date = new Date(dateStr + 'T12:00:00Z');
        if (isNaN(date.getTime())) return;

        let days = e.key === '-' ? -1 : 1;
        if (e.shiftKey) {
            days *= 7;
        }

        date.setUTCDate(date.getUTCDate() + days);
        setValue(date.toISOString().slice(0, 10));
    }
}
