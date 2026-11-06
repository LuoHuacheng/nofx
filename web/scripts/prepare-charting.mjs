import { cp, access, mkdir } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
const source = fileURLToPath(
  new URL('../vendor/charting/charting_library/', import.meta.url)
)
const target = fileURLToPath(
  new URL('../public/charting_library/', import.meta.url)
)
try {
  await access(source + 'charting_library.standalone.js')
} catch {
  throw new Error(
    'Chart source missing. Run git submodule update --init web/vendor/charting before building NOFX.'
  )
}
await mkdir(target, { recursive: true })
// Keep the vendor runtime unchanged; copying static assets is a build step.
await cp(source, target, { recursive: true })
console.log('Prepared pinned TradingView chart assets.')
