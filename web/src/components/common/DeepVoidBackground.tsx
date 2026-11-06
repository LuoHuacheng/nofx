import React from 'react'

interface DeepVoidBackgroundProps extends React.HTMLAttributes<HTMLDivElement> {
  children?: React.ReactNode
  className?: string
  disableAnimation?: boolean
}

// Retain the shared surface API while keeping workspace backgrounds quiet.
export function DeepVoidBackground({
  children,
  className = '',
  disableAnimation: _disableAnimation,
  ...props
}: DeepVoidBackgroundProps) {
  return (
    <div
      className={`relative w-full min-h-screen bg-nofx-bg text-nofx-text overflow-hidden flex flex-col ${className}`}
      {...props}
    >
      <div className="relative flex-1 flex flex-col h-full w-full">
        {children}
      </div>
    </div>
  )
}
