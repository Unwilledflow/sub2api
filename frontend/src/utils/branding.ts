import { sanitizeUrl } from '@/utils/url'

export const defaultBrandLogo = '/logo-v2.svg'

export function resolveBrandLogo(logoUrl: string): string {
  const sanitizedLogoUrl = sanitizeUrl(logoUrl, { allowRelative: true, allowDataUrl: true })
  return sanitizedLogoUrl || defaultBrandLogo
}

export function updateFavicon(logoUrl: string): void {
  const resolvedLogoUrl = resolveBrandLogo(logoUrl)
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!link) {
    link = document.createElement('link')
    link.rel = 'icon'
    document.head.appendChild(link)
  }
  link.type = resolvedLogoUrl.endsWith('.svg') ? 'image/svg+xml' : 'image/x-icon'
  link.href = resolvedLogoUrl
}
