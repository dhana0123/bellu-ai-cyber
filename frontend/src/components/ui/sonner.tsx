import { Toaster as Sonner, type ToasterProps } from 'sonner'

// Sonner toaster wired to Bellu tokens. Theme is read from the `dark` class on
// <html> (set by ThemeProvider) rather than next-themes, which this app doesn't
// use.
const Toaster = ({ ...props }: ToasterProps) => {
  const theme = document.documentElement.classList.contains('dark') ? 'dark' : 'light'
  return (
    <Sonner
      theme={theme}
      className="toaster group"
      style={
        {
          '--normal-bg': 'hsl(var(--popover))',
          '--normal-text': 'hsl(var(--popover-foreground))',
          '--normal-border': 'hsl(var(--border))',
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
