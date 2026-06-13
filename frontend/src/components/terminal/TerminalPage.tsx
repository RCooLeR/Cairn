import type { LucideIcon } from 'lucide-react';
import type {
  CheatsheetEntry,
  ContainerSummary,
  ProjectSummary,
  TerminalSessionInfo,
} from '../../../bindings/github.com/RCooLeR/Cairn/internal/models/models.js';
import type { ReactNode } from 'react';

import { Terminal as XTerm } from '@xterm/xterm';
import '@xterm/xterm/css/xterm.css';
import {
  Check,
  ChevronDown,
  Command,
  Container,
  Copy,
  FolderGit2,
  Play,
  Plus,
  Search,
  Server,
  ShieldAlert,
  Terminal as TerminalIcon,
  X,
} from 'lucide-react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { Clipboard, Events } from '@wailsio/runtime';

import { SettingsService, TerminalService } from '../../api/services';
import { Badge, Button, Card, CardBody, CardHeader, EmptyState, Modal } from '../ui';

type BadgeTone = 'ok' | 'warn' | 'error' | 'info' | 'neutral' | 'accent';

export type TerminalCommandRequest = {
  id: number;
  command: string;
};

type TerminalPageProps = {
  containers: ContainerSummary[];
  projects: ProjectSummary[];
  queuedCommand: TerminalCommandRequest | null;
  onCommandConsumed: (id: number) => void;
};

type PaletteNavItem<T extends string> = {
  id: T;
  label: string;
  icon: LucideIcon;
};

type CommandPaletteProps<T extends string> = {
  activePage: T;
  open: boolean;
  pages: PaletteNavItem<T>[];
  onClose: () => void;
  onNavigate: (page: T) => void;
  onRunSafeCommand: (command: string) => void;
};

type TerminalDataPayload = {
  sessionID: string;
  dataBase64: string;
};

type TerminalClosedPayload = {
  sessionID: string;
  exitCode: number;
};

type PasteGuardState = {
  sessionID: string;
  data: string;
};

type PendingRun = {
  command: string;
  sessionID: string;
};

type PlaceholderValues = Record<string, string>;

export function TerminalPage({
  containers,
  onCommandConsumed,
  projects,
  queuedCommand,
}: TerminalPageProps) {
  const [sessions, setSessions] = useState<TerminalSessionInfo[]>([]);
  const [activeSessionID, setActiveSessionID] = useState<string | null>(null);
  const [cheatsheet, setCheatsheet] = useState<CheatsheetEntry[]>([]);
  const [cheatsheetSearch, setCheatsheetSearch] = useState('');
  const [cheatsheetCategory, setCheatsheetCategory] = useState('all');
  const [selectedProjectID, setSelectedProjectID] = useState('');
  const [selectedContainerID, setSelectedContainerID] = useState('');
  const [shellOptions, setShellOptions] = useState<string[]>([]);
  const [containerShell, setContainerShell] = useState('');
  const [containerUser, setContainerUser] = useState('');
  const [containerWorkdir, setContainerWorkdir] = useState('');
  const [placeholderValues, setPlaceholderValues] =
    useState<PlaceholderValues>({});
  const [pendingRun, setPendingRun] = useState<PendingRun | null>(null);
  const [pasteGuard, setPasteGuard] = useState<PasteGuardState | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pendingTimer = useRef<number | null>(null);

  const activeSession = useMemo(
    () => sessions.find((session) => session.id === activeSessionID) ?? null,
    [activeSessionID, sessions],
  );
  const runningContainers = useMemo(
    () =>
      containers.filter((container) =>
        ['running', 'paused'].includes(container.state),
      ),
    [containers],
  );
  const categories = useMemo(() => {
    const unique = Array.from(
      new Set(cheatsheet.map((entry) => entry.category)),
    ).sort();
    return ['all', ...unique];
  }, [cheatsheet]);
  const filteredCheatsheet = useMemo(() => {
    const query = cheatsheetSearch.trim().toLowerCase();
    return cheatsheet.filter((entry) => {
      if (cheatsheetCategory !== 'all' && entry.category !== cheatsheetCategory) {
        return false;
      }
      if (!query) {
        return true;
      }
      return `${entry.category} ${entry.command} ${entry.description}`
        .toLowerCase()
        .includes(query);
    });
  }, [cheatsheet, cheatsheetCategory, cheatsheetSearch]);

  useEffect(() => {
    TerminalService.ListTerminalSessions()
      .then((nextSessions) => {
        const normalized = nextSessions ?? [];
        setSessions(normalized);
        setActiveSessionID((current) => current ?? normalized[0]?.id ?? null);
      })
      .catch((loadError: unknown) => {
        setError(errorMessage(loadError, 'Unable to load terminal sessions'));
      });
    SettingsService.GetCheatsheet()
      .then((entries) => setCheatsheet(entries ?? []))
      .catch((loadError: unknown) => {
        setError(errorMessage(loadError, 'Unable to load terminal cheatsheet'));
      });
  }, []);

  useEffect(() => {
    const off = Events.On('terminal:closed', (event) => {
      const payload = eventPayload<TerminalClosedPayload>(event);
      if (!payload) {
        return;
      }
      setSessions((current) =>
        current.filter((session) => session.id !== payload.sessionID),
      );
      setActiveSessionID((current) => {
        if (current !== payload.sessionID) {
          return current;
        }
        const next = sessions.find((session) => session.id !== payload.sessionID);
        return next?.id ?? null;
      });
      setStatus(`Session exited with code ${payload.exitCode}`);
    });
    return () => off();
  }, [sessions]);

  useEffect(() => {
    if (!selectedContainerID) {
      return undefined;
    }
    let active = true;
    TerminalService.DetectContainerShells(selectedContainerID)
      .then((shells) => {
        if (!active) {
          return;
        }
        const nextShells = shells ?? [];
        setShellOptions(nextShells);
        setContainerShell((current) => current || nextShells[0] || '/bin/sh');
      })
      .catch(() => {
        if (active) {
          setShellOptions([]);
          setContainerShell('/bin/sh');
        }
      });
    return () => {
      active = false;
    };
  }, [selectedContainerID]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && pendingTimer.current !== null) {
        window.clearTimeout(pendingTimer.current);
        pendingTimer.current = null;
        setPendingRun(null);
        setStatus('Command cancelled');
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  useEffect(
    () => () => {
      if (pendingTimer.current !== null) {
        window.clearTimeout(pendingTimer.current);
      }
    },
    [],
  );

  const addSession = useCallback((session: TerminalSessionInfo | null) => {
    if (!session) {
      return null;
    }
    setSessions((current) => {
      if (current.some((item) => item.id === session.id)) {
        return current;
      }
      return [...current, session];
    });
    setActiveSessionID(session.id);
    return session;
  }, []);

  const openHost = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      addSession(await TerminalService.OpenHostTerminal({ cols: 120, rows: 30 }));
    } catch (openError: unknown) {
      setError(errorMessage(openError, 'Unable to open host terminal'));
    } finally {
      setBusy(false);
    }
  }, [addSession]);

  const openBackend = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      return addSession(
        await TerminalService.OpenBackendTerminal({ cols: 120, rows: 30 }),
      );
    } catch (openError: unknown) {
      setError(errorMessage(openError, 'Unable to open backend terminal'));
      return null;
    } finally {
      setBusy(false);
    }
  }, [addSession]);

  const openProject = useCallback(async () => {
    if (!selectedProjectID) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      addSession(
        await TerminalService.OpenProjectTerminal(selectedProjectID, {
          cols: 120,
          rows: 30,
        }),
      );
    } catch (openError: unknown) {
      setError(errorMessage(openError, 'Unable to open project terminal'));
    } finally {
      setBusy(false);
    }
  }, [addSession, selectedProjectID]);

  const openContainer = useCallback(async () => {
    if (!selectedContainerID) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      addSession(
        await TerminalService.OpenContainerTerminal(selectedContainerID, {
          shell: containerShell,
          user: containerUser,
          workingDir: containerWorkdir,
          cols: 120,
          rows: 30,
        }),
      );
    } catch (openError: unknown) {
      setError(errorMessage(openError, 'Unable to open container terminal'));
    } finally {
      setBusy(false);
    }
  }, [
    addSession,
    containerShell,
    containerUser,
    containerWorkdir,
    selectedContainerID,
  ]);

  const closeSession = useCallback(
    async (session: TerminalSessionInfo) => {
      if (session.kind === 'container') {
        const confirmed = window.confirm(
          `Close terminal for ${session.title}? The exec session will exit.`,
        );
        if (!confirmed) {
          return;
        }
      }
      await TerminalService.CloseTerminal(session.id);
      setSessions((current) =>
        current.filter((item) => item.id !== session.id),
      );
      setActiveSessionID((current) =>
        current === session.id
          ? sessions.find((item) => item.id !== session.id)?.id ?? null
          : current,
      );
    },
    [sessions],
  );

  const sendInput = useCallback(
    async (session: TerminalSessionInfo, data: string) => {
      if (shouldGuardPaste(session, data)) {
        setPasteGuard({ sessionID: session.id, data });
        return;
      }
      await TerminalService.WriteTerminal(session.id, encodeTerminalInput(data));
    },
    [],
  );

  const writeCommand = useCallback(async (sessionID: string, command: string) => {
    await TerminalService.WriteTerminal(
      sessionID,
      encodeTerminalInput(`${command}\r`),
    );
  }, []);

  const scheduleCommand = useCallback(
    async (command: string) => {
      const trimmed = command.trim();
      if (!trimmed) {
        return;
      }
      let targetID = activeSessionID;
      if (!targetID) {
        const opened = await openBackend();
        targetID = opened?.id ?? null;
      }
      if (!targetID) {
        return;
      }
      if (pendingTimer.current !== null) {
        window.clearTimeout(pendingTimer.current);
      }
      setPendingRun({ command: trimmed, sessionID: targetID });
      pendingTimer.current = window.setTimeout(() => {
        pendingTimer.current = null;
        setPendingRun(null);
        void writeCommand(targetID, trimmed);
      }, 1000);
    },
    [activeSessionID, openBackend, writeCommand],
  );

  useEffect(() => {
    if (!queuedCommand) {
      return undefined;
    }
    const timer = window.setTimeout(() => {
      void scheduleCommand(queuedCommand.command);
      onCommandConsumed(queuedCommand.id);
    }, 0);
    return () => window.clearTimeout(timer);
  }, [onCommandConsumed, queuedCommand, scheduleCommand]);

  const copyCommand = useCallback(async (command: string) => {
    await Clipboard.SetText(command);
    setStatus('Command copied');
  }, []);

  const runCheatsheetEntry = useCallback(
    (entry: CheatsheetEntry) => {
      const resolved = resolveCommand(entry, activeSession, placeholderValues);
      if (resolved.unresolved.length > 0) {
        setError(`Fill ${resolved.unresolved.join(', ')} before running`);
        return;
      }
      if (!entry.runnable || entry.risk !== 'safe') {
        void copyCommand(resolved.command);
        return;
      }
      void scheduleCommand(resolved.command);
    },
    [activeSession, copyCommand, placeholderValues, scheduleCommand],
  );

  return (
    <div className="grid min-h-[calc(100vh-9rem)] gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
      <section className="flex min-h-[620px] min-w-0 flex-col overflow-hidden rounded-card border border-border bg-bg-panel">
        <div className="flex min-h-12 flex-wrap items-center gap-2 border-b border-border px-3 py-2">
          <Button
            icon={<TerminalIcon size={15} />}
            loading={busy}
            onClick={() => {
              void openHost();
            }}
            size="sm"
            variant="secondary"
          >
            Host
          </Button>
          <Button
            icon={<Server size={15} />}
            loading={busy}
            onClick={() => {
              void openBackend();
            }}
            size="sm"
            variant="secondary"
          >
            Backend
          </Button>
          <div className="flex min-w-[220px] items-center gap-2">
            <select
              aria-label="Project terminal"
              className="h-9 min-w-0 flex-1 rounded-control border border-border bg-bg-inset px-2 text-sm"
              onChange={(event) => setSelectedProjectID(event.target.value)}
              value={selectedProjectID}
            >
              <option value="">Project</option>
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
            </select>
            <Button
              aria-label="Open project terminal"
              disabled={!selectedProjectID}
              icon={<FolderGit2 size={15} />}
              loading={busy}
              onClick={() => {
                void openProject();
              }}
              size="icon"
              variant="secondary"
            />
          </div>
          <div className="flex min-w-[360px] flex-1 flex-wrap items-center gap-2">
            <select
              aria-label="Container terminal"
              className="h-9 min-w-[150px] flex-1 rounded-control border border-border bg-bg-inset px-2 text-sm"
              onChange={(event) => {
                const nextID = event.target.value;
                setSelectedContainerID(nextID);
                if (!nextID) {
                  setShellOptions([]);
                  setContainerShell('');
                }
              }}
              value={selectedContainerID}
            >
              <option value="">Container</option>
              {runningContainers.map((container) => (
                <option key={container.id} value={container.id}>
                  {container.name}
                </option>
              ))}
            </select>
            <select
              aria-label="Container shell"
              className="h-9 w-32 rounded-control border border-border bg-bg-inset px-2 text-sm"
              onChange={(event) => setContainerShell(event.target.value)}
              value={containerShell}
            >
              {(shellOptions.length ? shellOptions : [containerShell || '/bin/sh'])
                .filter(Boolean)
                .map((shell) => (
                  <option key={shell} value={shell}>
                    {shell}
                  </option>
                ))}
            </select>
            <input
              aria-label="Container user"
              className="h-9 w-24 rounded-control border border-border bg-bg-inset px-2 text-sm"
              onChange={(event) => setContainerUser(event.target.value)}
              placeholder="user"
              value={containerUser}
            />
            <input
              aria-label="Container working directory"
              className="h-9 w-28 rounded-control border border-border bg-bg-inset px-2 text-sm"
              onChange={(event) => setContainerWorkdir(event.target.value)}
              placeholder="/workdir"
              value={containerWorkdir}
            />
            <Button
              disabled={!selectedContainerID}
              icon={<Plus size={15} />}
              loading={busy}
              onClick={() => {
                void openContainer();
              }}
              size="sm"
              variant="secondary"
            >
              Open
            </Button>
          </div>
        </div>

        <div className="flex min-h-11 items-center gap-2 overflow-x-auto border-b border-border bg-bg-inset px-2 py-2">
          {sessions.map((session) => (
            <button
              key={session.id}
              className={[
                'flex h-8 max-w-56 shrink-0 items-center gap-2 rounded-control border px-2 text-sm',
                activeSessionID === session.id
                  ? 'border-accent bg-accent/10 text-accent'
                  : 'border-border bg-bg-card text-text-secondary hover:text-text-primary',
              ].join(' ')}
              onClick={() => setActiveSessionID(session.id)}
              type="button"
            >
              <SessionIcon kind={session.kind} />
              <span className="truncate">{session.title}</span>
              {session.isRoot ? (
                <Badge tone="error">
                  <ShieldAlert size={11} /> root
                </Badge>
              ) : null}
              <span
                aria-label={`Close ${session.title}`}
                className="rounded p-0.5 hover:bg-bg-panel"
                onClick={(event) => {
                  event.stopPropagation();
                  void closeSession(session);
                }}
                role="button"
                tabIndex={0}
              >
                <X size={13} />
              </span>
            </button>
          ))}
          {sessions.length === 0 ? (
            <span className="text-sm text-text-muted">No terminal sessions</span>
          ) : null}
        </div>

        {activeSession ? (
          <div className="border-b border-border px-3 py-2 text-xs text-text-muted">
            <span className="font-medium text-text-secondary">
              {activeSession.title}
            </span>
            <span className="mx-2">·</span>
            <span>{activeSession.shell || 'shell'}</span>
            {activeSession.isRoot ? (
              <>
                <span className="mx-2">·</span>
                <span className="text-error">root</span>
              </>
            ) : null}
            {activeSession.workingDir ? (
              <>
                <span className="mx-2">·</span>
                <span>{activeSession.workingDir}</span>
              </>
            ) : null}
          </div>
        ) : null}

        <div className="relative min-h-0 flex-1 bg-[#070a0f]">
          {sessions.map((session) => (
            <TerminalSurface
              active={session.id === activeSessionID}
              key={session.id}
              onInput={sendInput}
              session={session}
            />
          ))}
          {sessions.length === 0 ? (
            <div className="absolute inset-0 flex items-center justify-center">
              <EmptyState
                body="Open a host, backend, project, or container terminal."
                icon={<TerminalIcon size={30} />}
                title="Terminal"
              />
            </div>
          ) : null}
        </div>

        <div className="flex min-h-9 items-center gap-3 border-t border-border px-3 text-xs text-text-muted">
          <span>{activeSession ? `${activeSession.kind} session` : 'idle'}</span>
          <span className="ml-auto">{status}</span>
          {error ? <span className="text-error">{error}</span> : null}
        </div>
      </section>

      <aside className="min-h-0 space-y-4">
        <Card>
          <CardHeader
            actions={
              <Badge tone="neutral">
                {filteredCheatsheet.length}/{cheatsheet.length}
              </Badge>
            }
            title="Cheatsheet"
          />
          <CardBody className="space-y-3">
            <div className="relative">
              <Search
                className="pointer-events-none absolute left-2 top-2.5 text-text-muted"
                size={15}
              />
              <input
                aria-label="Search cheatsheet"
                className="h-9 w-full rounded-control border border-border bg-bg-inset pl-8 pr-2 text-sm"
                onChange={(event) => setCheatsheetSearch(event.target.value)}
                value={cheatsheetSearch}
              />
            </div>
            <div className="flex gap-2 overflow-x-auto pb-1">
              {categories.map((category) => (
                <button
                  className={[
                    'h-8 shrink-0 rounded-control border px-2 text-xs',
                    cheatsheetCategory === category
                      ? 'border-accent bg-accent/10 text-accent'
                      : 'border-border bg-bg-inset text-text-secondary',
                  ].join(' ')}
                  key={category}
                  onClick={() => setCheatsheetCategory(category)}
                  type="button"
                >
                  {category}
                </button>
              ))}
            </div>
            <div className="max-h-[520px] space-y-2 overflow-auto pr-1">
              {filteredCheatsheet.map((entry) => (
                <CheatsheetRow
                  activeSession={activeSession}
                  entry={entry}
                  key={`${entry.category}:${entry.command}`}
                  onCopy={copyCommand}
                  onPlaceholderChange={(name, value) =>
                    setPlaceholderValues((current) => ({
                      ...current,
                      [name]: value,
                    }))
                  }
                  onRun={runCheatsheetEntry}
                  placeholderValues={placeholderValues}
                />
              ))}
            </div>
          </CardBody>
        </Card>
      </aside>

      <Modal
        footer={
          <>
            <Button onClick={() => setPasteGuard(null)} variant="secondary">
              Cancel
            </Button>
            <Button
              onClick={() => {
                if (!pasteGuard) {
                  return;
                }
                const guard = pasteGuard;
                setPasteGuard(null);
                void TerminalService.WriteTerminal(
                  guard.sessionID,
                  encodeTerminalInput(guard.data),
                );
              }}
              variant="primary"
            >
              Paste
            </Button>
          </>
        }
        onClose={() => setPasteGuard(null)}
        open={Boolean(pasteGuard)}
        title="Confirm Paste"
      >
        <pre className="max-h-64 overflow-auto rounded-control bg-bg-inset p-3 text-xs text-text-secondary">
          {pasteGuard?.data}
        </pre>
      </Modal>

      {pendingRun ? (
        <div className="fixed bottom-5 right-5 z-50 max-w-md rounded-card border border-accent bg-bg-panel p-3 shadow-xl">
          <div className="flex items-center gap-2 text-sm text-text-primary">
            <Play size={15} />
            <span className="truncate font-mono">{pendingRun.command}</span>
          </div>
          <div className="mt-2 flex justify-end">
            <Button
              onClick={() => {
                if (pendingTimer.current !== null) {
                  window.clearTimeout(pendingTimer.current);
                }
                pendingTimer.current = null;
                setPendingRun(null);
              }}
              size="sm"
              variant="secondary"
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  );
}

export function CommandPalette<T extends string>({
  activePage,
  onClose,
  onNavigate,
  onRunSafeCommand,
  open,
  pages,
}: CommandPaletteProps<T>) {
  const [query, setQuery] = useState('');
  const [commands, setCommands] = useState<CheatsheetEntry[]>([]);

  useEffect(() => {
    if (!open) {
      return;
    }
    SettingsService.GetCheatsheet()
      .then((entries) => setCommands(entries ?? []))
      .catch(() => setCommands([]));
  }, [open]);

  const filteredPages = pages.filter((page) =>
    page.label.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const filteredCommands = commands
    .filter((entry) => {
      const haystack = `${entry.command} ${entry.description}`.toLowerCase();
      return haystack.includes(query.trim().toLowerCase());
    })
    .slice(0, 8);

  if (!open) {
    return null;
  }

  return (
    <div
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/55 px-4 py-20"
      role="dialog"
    >
      <div className="w-full max-w-2xl overflow-hidden rounded-card border border-border bg-bg-panel shadow-2xl">
        <div className="flex h-12 items-center gap-2 border-b border-border px-3">
          <Command size={17} />
          <input
            autoFocus
            className="h-full flex-1 bg-transparent text-sm outline-none"
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                onClose();
              }
            }}
            placeholder="Search"
            value={query}
          />
          <Button
            aria-label="Close palette"
            icon={<X size={15} />}
            onClick={onClose}
            size="icon"
            variant="ghost"
          />
        </div>
        <div className="max-h-[520px] overflow-auto p-2">
          <PaletteSection title="Navigation">
            {filteredPages.map((page) => {
              const Icon = page.icon;
              return (
                <button
                  className="flex h-10 w-full items-center gap-3 rounded-control px-2 text-left text-sm hover:bg-bg-card"
                  key={page.id}
                  onClick={() => {
                    onNavigate(page.id);
                    onClose();
                  }}
                  type="button"
                >
                  <Icon size={16} />
                  <span>{page.label}</span>
                  {page.id === activePage ? (
                    <Check className="ml-auto text-accent" size={15} />
                  ) : null}
                </button>
              );
            })}
          </PaletteSection>
          <PaletteSection title="Commands">
            {filteredCommands.map((entry) => (
              <button
                className="flex min-h-11 w-full items-center gap-3 rounded-control px-2 text-left text-sm hover:bg-bg-card"
                key={`${entry.category}:${entry.command}`}
                onClick={() => {
                  if (entry.runnable && entry.risk === 'safe') {
                    onRunSafeCommand(entry.command);
                  } else {
                    void Clipboard.SetText(entry.command);
                  }
                  onClose();
                }}
                type="button"
              >
                <TerminalIcon size={16} />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono text-xs">
                    {entry.command}
                  </span>
                  <span className="block truncate text-xs text-text-muted">
                    {entry.description}
                  </span>
                </span>
                <Badge tone={riskTone(entry.risk)}>{entry.risk}</Badge>
                <ChevronDown className="rotate-[-90deg]" size={14} />
              </button>
            ))}
          </PaletteSection>
        </div>
      </div>
    </div>
  );
}

function TerminalSurface({
  active,
  onInput,
  session,
}: {
  active: boolean;
  onInput: (session: TerminalSessionInfo, data: string) => Promise<void>;
  session: TerminalSessionInfo;
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<XTerm | null>(null);
  const resizeTimer = useRef<number | null>(null);

  useEffect(() => {
    if (!hostRef.current) {
      return undefined;
    }
    const terminal = new XTerm({
      allowProposedApi: false,
      convertEol: true,
      cursorBlink: true,
      fontFamily:
        'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
      fontSize: 13,
      scrollback: 10000,
      theme: {
        background: '#070a0f',
        foreground: '#d6deeb',
        cursor: '#2dd4a7',
        selectionBackground: '#2dd4a744',
      },
    });
    terminal.open(hostRef.current);
    terminalRef.current = terminal;
    const disposable = terminal.onData((data) => {
      void onInput(session, data);
    });
    const resize = () => {
      if (!hostRef.current) {
        return;
      }
      const rect = hostRef.current.getBoundingClientRect();
      const cols = Math.max(40, Math.floor(rect.width / 8.2));
      const rows = Math.max(10, Math.floor(rect.height / 17.5));
      terminal.resize(cols, rows);
      if (resizeTimer.current !== null) {
        window.clearTimeout(resizeTimer.current);
      }
      resizeTimer.current = window.setTimeout(() => {
        void TerminalService.ResizeTerminal(session.id, cols, rows);
      }, 100);
    };
    resize();
    let observer: ResizeObserver | null = null;
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(resize);
      observer.observe(hostRef.current);
    }
    return () => {
      if (resizeTimer.current !== null) {
        window.clearTimeout(resizeTimer.current);
      }
      observer?.disconnect();
      disposable.dispose();
      terminal.dispose();
      terminalRef.current = null;
    };
  }, [onInput, session]);

  useEffect(() => {
    const off = Events.On('terminal:data', (event) => {
      const payload = eventPayload<TerminalDataPayload>(event);
      if (!payload || payload.sessionID !== session.id) {
        return;
      }
      terminalRef.current?.write(decodeBase64(payload.dataBase64));
    });
    return () => off();
  }, [session.id]);

  return (
    <div
      className={active ? 'absolute inset-0 p-2' : 'hidden'}
      data-terminal-session={session.id}
      ref={hostRef}
    />
  );
}

function CheatsheetRow({
  activeSession,
  entry,
  onCopy,
  onPlaceholderChange,
  onRun,
  placeholderValues,
}: {
  activeSession: TerminalSessionInfo | null;
  entry: CheatsheetEntry;
  onCopy: (command: string) => void;
  onPlaceholderChange: (name: string, value: string) => void;
  onRun: (entry: CheatsheetEntry) => void;
  placeholderValues: PlaceholderValues;
}) {
  const resolved = resolveCommand(entry, activeSession, placeholderValues);
  return (
    <div className="rounded-control border border-border bg-bg-inset p-2">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-xs text-text-primary">
            {resolved.command}
          </div>
          <div className="mt-1 text-xs text-text-muted">
            {entry.description}
          </div>
        </div>
        <Badge tone={riskTone(entry.risk)}>{entry.risk}</Badge>
      </div>
      {resolved.unresolved.length > 0 ? (
        <div className="mt-2 grid gap-2">
          {resolved.unresolved.map((name) => (
            <input
              aria-label={`${name} value`}
              className="h-8 rounded-control border border-border bg-bg-panel px-2 text-xs"
              key={name}
              onChange={(event) => onPlaceholderChange(name, event.target.value)}
              placeholder={name}
              value={placeholderValues[name] ?? ''}
            />
          ))}
        </div>
      ) : null}
      <div className="mt-2 flex justify-end gap-2">
        <Button
          icon={<Copy size={13} />}
          onClick={() => onCopy(resolved.command)}
          size="sm"
          variant="ghost"
        >
          Copy
        </Button>
        <Button
          disabled={
            !entry.runnable ||
            entry.risk !== 'safe' ||
            resolved.unresolved.length > 0
          }
          icon={<Play size={13} />}
          onClick={() => onRun(entry)}
          size="sm"
          variant="secondary"
        >
          Run
        </Button>
      </div>
    </div>
  );
}

function PaletteSection({
  children,
  title,
}: {
  children: ReactNode;
  title: string;
}) {
  return (
    <section className="mb-2">
      <div className="px-2 py-1 text-[11px] uppercase text-text-muted">
        {title}
      </div>
      <div>{children}</div>
    </section>
  );
}

function SessionIcon({ kind }: { kind: string }) {
  if (kind === 'container') {
    return <Container size={14} />;
  }
  if (kind === 'project') {
    return <FolderGit2 size={14} />;
  }
  if (kind === 'backend') {
    return <Server size={14} />;
  }
  return <TerminalIcon size={14} />;
}

function resolveCommand(
  entry: CheatsheetEntry,
  activeSession: TerminalSessionInfo | null,
  placeholderValues: PlaceholderValues,
) {
  const unresolved = new Set<string>();
  const command = entry.command.replace(/<([^>]+)>/g, (match, rawName) => {
    const name = String(rawName);
    const explicit = placeholderValues[name]?.trim();
    if (explicit) {
      return explicit;
    }
    if (name === 'container' && activeSession?.containerID) {
      return activeSession.containerID;
    }
    if (name === 'service' && activeSession?.title) {
      return activeSession.title;
    }
    unresolved.add(name);
    return match;
  });
  return { command, unresolved: Array.from(unresolved) };
}

function shouldGuardPaste(session: TerminalSessionInfo, data: string) {
  if (session.kind !== 'container' && !session.isRoot) {
    return false;
  }
  const normalized = data.replace(/\r/g, '\n');
  const lines = normalized.split('\n').filter((line) => line.trim() !== '');
  return lines.length > 1;
}

function encodeTerminalInput(value: string) {
  const bytes = new TextEncoder().encode(value);
  let binary = '';
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte);
  });
  return btoa(binary);
}

function decodeBase64(value: string) {
  try {
    return atob(value);
  } catch {
    return '';
  }
}

function eventPayload<T>(event: unknown): T | null {
  if (!event) {
    return null;
  }
  if (typeof event === 'object' && 'data' in event) {
    return ((event as { data?: T }).data ?? null) as T | null;
  }
  return event as T;
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

function riskTone(risk: string): BadgeTone {
  if (risk === 'safe') {
    return 'ok';
  }
  if (risk === 'needs_confirmation') {
    return 'warn';
  }
  if (risk === 'destructive' || risk === 'dangerous') {
    return 'error';
  }
  return 'neutral';
}
