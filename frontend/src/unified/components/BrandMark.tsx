interface BrandMarkProps {
  className?: string
  /** Rendered size in CSS pixels; the asset is 192px so it stays crisp at 2x. */
  size?: number
}

/** The Yufolo mascot. Decorative next to the brand name, so it carries no alt text. */
export function BrandMark({ className, size = 38 }: BrandMarkProps) {
  return (
    <img
      alt=""
      className={className ? `dt-brand-mark ${className}` : 'dt-brand-mark'}
      decoding="async"
      draggable={false}
      height={size}
      src="/brand/yufolo-mark-192.png"
      width={size}
    />
  )
}
