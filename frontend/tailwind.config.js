/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        /* ------------------------------------------------------------------
           「Graphite」主题 — 高对比数据密度
           ------------------------------------------------------------------
           这三套色板是全站换肤的主杠杆：primary 被引用 1775 次、dark 3129 次，
           改这里等于一次性换掉全站配色。色阶编号沿用 Tailwind 惯例
           （50 最浅 → 950 最深），所以既有 class 不需要改一行。

           primary —— 电光靛蓝。500 是纯 UI 色（按钮底、图表主线），
           600 才够深到在白底上做正文级强调（对比度 ~5.6:1）。
           400 在近黑底上是高对比强调色。
        ------------------------------------------------------------------ */
        primary: {
          50: '#f2f4ff',
          100: '#e5e9ff',
          200: '#ccd4ff',
          300: '#a6b2ff',
          400: '#7d87ff',
          500: '#5b60f5',
          600: '#4640e0',
          700: '#3a33bd',
          800: '#302c96',
          900: '#2b2877',
          950: '#1a1846'
        },

        /* accent —— 冷却青，仅用于图表次要序列与渐变收尾，不参与正文。 */
        accent: {
          50: '#ecfeff',
          100: '#cffafe',
          200: '#a5f3fc',
          300: '#67e8f9',
          400: '#22d3ee',
          500: '#06b6d4',
          600: '#0891b2',
          700: '#0e7490',
          800: '#155e75',
          900: '#164e63',
          950: '#083344'
        },

        /* dark —— 石墨中性阶。注意它被同时用作背景与文字：
           bg-dark-800 是卡片面，text-dark-400 是暗色模式下的次要文字，
           border-dark-700 是分隔线。所以低号必须够亮、高号必须够深。 */
        dark: {
          50: '#f6f7f9',
          100: '#eceef2',
          200: '#d5d9e0',
          300: '#b0b7c3',
          400: '#858d9c',
          500: '#626a78',
          600: '#3d434f',
          700: '#262b35',
          800: '#14171e',
          900: '#0c0e13',
          950: '#07080b'
        }
      },

      fontFamily: {
        sans: [
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },

      boxShadow: {
        /* 数据密度风：阴影更短、更硬，靠边框而不是靠光晕分隔层次。 */
        glass: '0 8px 32px rgba(0, 0, 0, 0.08)',
        'glass-sm': '0 4px 16px rgba(0, 0, 0, 0.06)',
        glow: '0 0 0 1px rgba(91, 96, 245, 0.35), 0 0 22px rgba(91, 96, 245, 0.28)',
        'glow-lg': '0 0 0 1px rgba(91, 96, 245, 0.45), 0 0 44px rgba(91, 96, 245, 0.38)',
        card: '0 1px 2px rgba(9, 12, 20, 0.06), 0 1px 1px rgba(9, 12, 20, 0.04)',
        'card-hover': '0 12px 36px -8px rgba(9, 12, 20, 0.18), 0 2px 8px -2px rgba(9, 12, 20, 0.08)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)',
        /* 新增：高对比主题专用的抬升与聚焦环 */
        xs: '0 1px 1px rgba(9, 12, 20, 0.05)',
        lift: '0 16px 40px -12px rgba(9, 12, 20, 0.28), 0 4px 12px -4px rgba(9, 12, 20, 0.12)',
        'lift-dark': '0 16px 44px -12px rgba(0, 0, 0, 0.72), 0 4px 14px -4px rgba(0, 0, 0, 0.5)',
        focus: '0 0 0 3px rgba(91, 96, 245, 0.28)'
      },

      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-primary':
          'linear-gradient(135deg, #7d87ff 0%, #5b60f5 52%, #4640e0 100%)',
        'gradient-dark': 'linear-gradient(135deg, #262b35 0%, #14171e 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        /* 网格纹理：数据密度风的背景质感，比柔和光斑更有“仪表盘”味。 */
        'grid-fade':
          'linear-gradient(to right, rgba(120,130,150,0.08) 1px, transparent 1px), linear-gradient(to bottom, rgba(120,130,150,0.08) 1px, transparent 1px)',
        'mesh-gradient':
          'radial-gradient(at 38% 12%, rgba(91, 96, 245, 0.14) 0px, transparent 46%), radial-gradient(at 82% 0%, rgba(6, 182, 212, 0.10) 0px, transparent 44%), radial-gradient(at 0% 62%, rgba(70, 64, 224, 0.08) 0px, transparent 42%)'
      },

      /* 更紧的圆角：卡片从 16px 收到 10px，按钮从 12px 收到 8px。
         密集表格里大圆角会显得松散。 */
      borderRadius: {
        xs: '3px',
        sm: '5px',
        DEFAULT: '7px',
        md: '8px',
        lg: '10px',
        xl: '12px',
        '2xl': '14px',
        '3xl': '18px',
        '4xl': '2rem'
      },

      transitionTimingFunction: {
        /* 主题级缓动：进场用超冲（expo），交互反馈用回弹（spring）。 */
        expo: 'cubic-bezier(0.16, 1, 0.3, 1)',
        spring: 'cubic-bezier(0.34, 1.56, 0.64, 1)',
        snap: 'cubic-bezier(0.22, 1, 0.36, 1)'
      },

      animation: {
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s cubic-bezier(0.16, 1, 0.3, 1)',
        'slide-down': 'slideDown 0.3s cubic-bezier(0.16, 1, 0.3, 1)',
        'slide-in-right': 'slideInRight 0.3s cubic-bezier(0.16, 1, 0.3, 1)',
        'scale-in': 'scaleIn 0.2s cubic-bezier(0.34, 1.56, 0.64, 1)',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'glow 2s ease-in-out infinite alternate'
      },

      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(91, 96, 245, 0.22)' },
          '100%': { boxShadow: '0 0 30px rgba(91, 96, 245, 0.36)' }
        }
      },

      backdropBlur: {
        xs: '2px'
      }
    }
  },
  plugins: []
}
