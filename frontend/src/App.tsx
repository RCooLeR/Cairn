import {
  Activity,
  Bell,
  Box,
  ChevronLeft,
  Container,
  Database,
  Gauge,
  GitBranch,
  HardDrive,
  Layers,
  Network,
  Search,
  Settings,
  SquareTerminal,
} from 'lucide-react';
import { useEffect } from 'react';

import type { LucideIcon } from 'lucide-react';

import { getAppVersion } from './api/app';
import { useAppStore } from './state/appStore';

const logoUrl = '/cairn-logo.png';

type NavItem = {
  label: string;
  active?: boolean;
  badge?: string;
  icon: LucideIcon;
};

const navItems: NavItem[] = [
  { label: 'Overview', icon: Gauge, active: true },
  { label: 'Projects', icon: Layers },
  { label: 'Containers', icon: Container },
  { label: 'Images', icon: Box },
  { label: 'Volumes', icon: Database },
  { label: 'Networks', icon: Network },
  { label: 'Logs', icon: GitBranch },
  { label: 'Terminal', icon: SquareTerminal },
  { label: 'Updates', icon: Activity, badge: '0' },
  { label: 'Settings', icon: Settings },
];

const metricCards = [
  { label: 'Projects', value: '0', hint: 'No provider connected' },
  { label: 'Containers', value: '0', hint: 'Waiting for Docker' },
  { label: 'Images', value: '0', hint: 'Cache empty' },
  { label: 'Volumes', value: '0', hint: 'Cache empty' },
];

function StatusDot() {
  return <span className="h-2 w-2 rounded-full bg-neutral" aria-hidden="true" />;
}

function App() {
  const version = useAppStore((state) => state.version);
  const setVersion = useAppStore((state) => state.setVersion);
  const setVersionError = useAppStore((state) => state.setVersionError);
  const setVersionLoading = useAppStore((state) => state.setVersionLoading);

  useEffect(() => {
    let active = true;
    setVersionLoading(true);

    getAppVersion()
      .then((nextVersion) => {
        if (active) {
          setVersion(nextVersion);
        }
      })
      .catch((error: unknown) => {
        if (active) {
          setVersionError(error instanceof Error ? error.message : 'Unable to load app version');
        }
      })
      .finally(() => {
        if (active) {
          setVersionLoading(false);
        }
      });

    return () => {
      active = false;
    };
  }, [setVersion, setVersionError, setVersionLoading]);

  const versionLabel = version?.version ? `v${version.version}` : 'v1.0 workspace';

  return (
    <main className="min-h-screen bg-bg-app text-text-primary">
      <div className="grid min-h-screen grid-cols-[220px_1fr]">
        <aside className="flex min-h-screen flex-col border-r border-border bg-bg-panel">
          <div className="flex h-16 items-center gap-3 border-b border-border px-4">
            <img src={logoUrl} alt="Cairn" className="h-9 w-9 rounded-lg" />
            <div>
              <div className="text-sm font-semibold">Cairn</div>
              <div className="text-xs text-text-muted">{versionLabel}</div>
            </div>
          </div>

          <nav className="flex-1 space-y-1 px-2 py-3" aria-label="Main navigation">
            {navItems.map((item) => {
              const Icon = item.icon;
              return (
                <button
                  key={item.label}
                  className={[
                    'flex h-10 w-full items-center gap-3 rounded-control px-3 text-left text-sm transition',
                    item.active
                      ? 'bg-accent/12 text-accent shadow-[inset_3px_0_0_rgb(45_212_167)]'
                      : 'text-text-secondary hover:bg-bg-card hover:text-text-primary',
                  ].join(' ')}
                  type="button"
                >
                  <Icon size={18} strokeWidth={1.8} />
                  <span className="flex-1 truncate">{item.label}</span>
                  {item.badge ? (
                    <span className="rounded-full border border-border bg-bg-inset px-1.5 text-xs text-text-muted">
                      {item.badge}
                    </span>
                  ) : null}
                </button>
              );
            })}
          </nav>

          <div className="border-t border-border p-3">
            <div className="rounded-card border border-border bg-bg-inset p-3">
              <div className="flex items-center gap-2 text-sm">
                <StatusDot />
                <span className="font-medium">Docker Engine</span>
                <span className="ml-auto text-xs text-text-muted">Stopped</span>
              </div>
              <div className="mt-2 truncate font-mono text-xs text-text-muted">No provider selected</div>
              <button
                type="button"
                className="mt-3 inline-flex h-8 items-center justify-center gap-2 rounded-control border border-border px-3 text-xs text-text-secondary hover:border-border-strong hover:text-text-primary"
              >
                <ChevronLeft size={14} />
                Repair
              </button>
            </div>
          </div>
        </aside>

        <section className="min-w-0">
          <header className="flex h-16 items-center justify-between border-b border-border bg-bg-app px-6">
            <div>
              <h1 className="text-xl font-semibold tracking-normal">Overview</h1>
              <p className="text-sm text-text-muted">Provider state and Docker inventory</p>
            </div>
            <div className="flex items-center gap-2">
              <button
                type="button"
                className="inline-flex h-9 w-9 items-center justify-center rounded-control border border-border text-text-secondary hover:border-border-strong hover:text-text-primary"
                aria-label="Search"
              >
                <Search size={17} />
              </button>
              <button
                type="button"
                className="inline-flex h-9 w-9 items-center justify-center rounded-control border border-border text-text-secondary hover:border-border-strong hover:text-text-primary"
                aria-label="Notifications"
              >
                <Bell size={17} />
              </button>
            </div>
          </header>

          <div className="border-b border-border bg-warn/10 px-6 py-3 text-sm text-warn">
            Docker is not reachable
          </div>

          <div className="space-y-6 p-6">
            <section className="grid grid-cols-4 gap-4" aria-label="Docker object counts">
              {metricCards.map((card) => (
                <article key={card.label} className="rounded-card border border-border bg-bg-card p-4">
                  <div className="text-sm text-text-secondary">{card.label}</div>
                  <div className="mt-3 text-2xl font-semibold">{card.value}</div>
                  <div className="mt-2 text-xs text-text-muted">{card.hint}</div>
                </article>
              ))}
            </section>

            <section className="grid grid-cols-[1.2fr_0.8fr] gap-4">
              <article className="rounded-card border border-border bg-bg-card">
                <div className="flex items-center justify-between border-b border-border px-4 py-3">
                  <h2 className="text-sm font-semibold">Provider Health</h2>
                  <span className="rounded-full bg-neutral/15 px-2 py-1 text-xs text-text-muted">Unknown</span>
                </div>
                <div className="grid grid-cols-3 gap-3 p-4">
                  {['Docker', 'Compose', 'Buildx'].map((item) => (
                    <div key={item} className="rounded-control border border-border bg-bg-inset p-3">
                      <div className="flex items-center gap-2 text-sm">
                        <StatusDot />
                        <span>{item}</span>
                      </div>
                      <div className="mt-2 font-mono text-xs text-text-muted">not detected</div>
                    </div>
                  ))}
                </div>
              </article>

              <article className="rounded-card border border-border bg-bg-card">
                <div className="flex items-center justify-between border-b border-border px-4 py-3">
                  <h2 className="text-sm font-semibold">Disk Usage</h2>
                  <HardDrive size={16} className="text-text-muted" />
                </div>
                <div className="p-4">
                  <div className="h-32 rounded-control border border-dashed border-border bg-bg-inset" />
                  <div className="mt-3 text-xs text-text-muted">No samples</div>
                </div>
              </article>
            </section>
          </div>
        </section>
      </div>
    </main>
  );
}

export default App;
