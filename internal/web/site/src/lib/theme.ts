import { useCallback, useEffect, useState } from 'react'

export type Theme = 'dark' | 'light'

const storageKey = 'horizon.theme'
const lightClass = 'light'

function storedTheme(): Theme {
  try {
    return localStorage.getItem(storageKey) === lightClass ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}

export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle(lightClass, theme === 'light')
  try {
    localStorage.setItem(storageKey, theme)
  } catch {
    return
  }
}

export function useTheme(): { theme: Theme; setTheme: (theme: Theme) => void } {
  const [theme, setThemeState] = useState<Theme>(storedTheme)

  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  const setTheme = useCallback((next: Theme) => {
    setThemeState(next)
  }, [])

  return { theme, setTheme }
}
