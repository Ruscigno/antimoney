import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';

type ShortcutAction = () => void;

interface Shortcut {
    key: string;
    ctrl?: boolean;
    alt?: boolean;
    shift?: boolean;
    action: ShortcutAction;
    description: string;
    scope?: 'global' | 'modal';
}

// Global registry for shortcuts — components register/unregister
const shortcuts: Shortcut[] = [];

export function registerShortcut(shortcut: Shortcut) {
    shortcuts.push(shortcut);
    return () => {
        const idx = shortcuts.indexOf(shortcut);
        if (idx >= 0) shortcuts.splice(idx, 1);
    };
}

export function getShortcuts(): Shortcut[] {
    return [...shortcuts];
}

function handleKeyDown(e: KeyboardEvent) {
    // Don't trigger shortcuts when typing in inputs
    const target = e.target as HTMLElement;
    const isInput = target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT';

    for (const s of shortcuts) {
        if (s.key.toLowerCase() !== e.key.toLowerCase()) continue;
        if (s.ctrl && !e.ctrlKey && !e.metaKey) continue;
        if (s.alt && !e.altKey) continue;
        if (s.shift && !e.shiftKey) continue;

        // ESC always works; other shortcuts skip if in an input
        if (s.key !== 'Escape' && isInput) continue;

        e.preventDefault();
        e.stopPropagation();
        s.action();
        return;
    }
}

let initialized = false;

export function useGlobalShortcuts() {
    const navigate = useNavigate();

    useEffect(() => {
        if (initialized) return;
        initialized = true;

        document.addEventListener('keydown', handleKeyDown, true);

        // Register global navigation shortcuts
        const unregister = [
            registerShortcut({
                key: '1', alt: true,
                action: () => navigate('/'),
                description: 'Go to Dashboard',
                scope: 'global',
            }),
            registerShortcut({
                key: '2', alt: true,
                action: () => navigate('/accounts'),
                description: 'Go to Accounts',
                scope: 'global',
            }),
            registerShortcut({
                key: '3', alt: true,
                action: () => navigate('/transactions'),
                description: 'Go to Transactions',
                scope: 'global',
            }),
            registerShortcut({
                key: '4', alt: true,
                action: () => navigate('/data'),
                description: 'Go to Data Management',
                scope: 'global',
            }),
        ];

        return () => {
            document.removeEventListener('keydown', handleKeyDown, true);
            unregister.forEach(u => u());
            initialized = false;
        };
    }, [navigate]);
}

/**
 * Hook to register / unregister shortcuts tied to a component lifecycle.
 */
export function useShortcut(
    key: string,
    action: ShortcutAction,
    description: string,
    modifiers?: { ctrl?: boolean; alt?: boolean; shift?: boolean },
    deps: unknown[] = [],
) {
    useEffect(() => {
        const unreg = registerShortcut({
            key,
            ...modifiers,
            action,
            description,
        });
        return unreg;
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, deps);
}
