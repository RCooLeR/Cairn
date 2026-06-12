/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        'bg-app': '#0D1117',
        'bg-panel': '#111820',
        'bg-card': '#161F29',
        'bg-inset': '#0B1016',
        border: 'rgba(255,255,255,0.08)',
        'border-strong': 'rgba(255,255,255,0.18)',
        'text-primary': '#F0F6FC',
        'text-secondary': 'rgba(240,246,252,0.68)',
        'text-muted': 'rgba(240,246,252,0.44)',
        accent: '#2DD4A7',
        ok: '#2DD4A7',
        warn: '#F5B83D',
        error: '#F0605D',
        info: '#4D9FFF',
        neutral: '#8B949E',
      },
      borderRadius: {
        card: '12px',
        control: '8px',
      },
      fontFamily: {
        mono: ['"JetBrains Mono"', 'ui-monospace', 'SFMono-Regular', 'Consolas', 'monospace'],
      },
    },
  },
  plugins: [],
};
