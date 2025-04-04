// tailwind.config.js
/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        'radar-blue': '#0066cc',
        'radar-dark': '#1a2b3c',
        'radar-light': '#f0f4f8',
        'radar-accent': '#00a3e0',
        'radar-warning': '#ffc107',
        'radar-danger': '#dc3545',
        'radar-success': '#28a745',
      },
      fontFamily: {
        sans: ['Inter', 'sans-serif'],
        mono: ['JetBrains Mono', 'monospace'],
      },
      animation: {
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
      }
    },
  },
  plugins: [],
}