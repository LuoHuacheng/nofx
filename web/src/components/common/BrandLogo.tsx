interface BrandLogoProps {
  compact?: boolean
  className?: string
}

/** The approved Flow identity, with a vector mark for crisp small-size use. */
export function BrandLogo({ compact = false, className = '' }: BrandLogoProps) {
  return (
    <span className={`nofx-brand ${className}`} aria-label="NOFX">
      <img src="/icons/nofx-flow.svg" alt="" width="38" height="33" />
      {!compact && (
        <span className="nofx-wordmark" aria-hidden="true">
          nofx
        </span>
      )}
    </span>
  )
}
