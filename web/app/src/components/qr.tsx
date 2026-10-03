import qrcode from 'qrcode-generator'
import { useMemo } from 'react'

/** Draws `text` as a QR code (an SVG path, so it scales and stays sharp). Always dark on white so scanners can read it. */
export function QrCode({ text, label, size = 160 }: { text: string; label: string; size?: number }) {
  const { n, path } = useMemo(() => {
    const qr = qrcode(0, 'M')
    qr.addData(text)
    qr.make()
    const count = qr.getModuleCount()
    let d = ''
    for (let r = 0; r < count; r++) {
      for (let c = 0; c < count; c++) {
        if (qr.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`
      }
    }
    return { n: count, path: d }
  }, [text])
  const quiet = 3 // modules of white border, as the QR specification asks
  const box = n + quiet * 2
  return (
    <svg
      className="qr"
      role="img"
      aria-label={label}
      width={size}
      height={size}
      viewBox={`${-quiet} ${-quiet} ${box} ${box}`}
      shapeRendering="crispEdges"
    >
      <rect x={-quiet} y={-quiet} width={box} height={box} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  )
}
