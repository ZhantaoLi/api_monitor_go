// Shared Tailwind CSS configuration for all pages
// Unified zinc + neon theme across dashboard, log viewer, and analysis
tailwind.config = {
    darkMode: 'class',
    theme: {
        extend: {
            fontFamily: {
                sans: ['Inter', 'sans-serif'],
            },
            fontSize: {
                'xxs': '0.65rem',
            },
            colors: {
                // Custom zinc shades (dark backgrounds)
                zinc: { 850: '#26262a', 900: '#1e1e21', 950: '#121215' },
                // Neon accent palette (unified across all pages)
                neon: {
                    blue: '#00f3ff',
                    pink: '#ff00ff',
                    purple: '#9d00ff',
                    green: '#2be8a5',
                }
            },
            animation: {
                'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
            }
        }
    }
}
