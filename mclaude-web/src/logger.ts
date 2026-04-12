import pino from 'pino'

// In browser context, process is not available; default to 'info'
const logLevel = (() => {
  try {
    return (typeof process !== 'undefined' && process.env?.['LOG_LEVEL']) || 'info'
  } catch {
    return 'info'
  }
})()

export const logger = pino({
  level: logLevel,
  browser: {
    asObject: true,
  },
})
