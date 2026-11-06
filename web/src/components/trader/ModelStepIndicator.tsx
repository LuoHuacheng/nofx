import React from 'react'
import { Check } from 'lucide-react'

interface ModelStepIndicatorProps {
  currentStep: number
  labels: string[]
}

export function ModelStepIndicator({
  currentStep,
  labels,
}: ModelStepIndicatorProps) {
  return (
    <div className="flex items-center justify-center gap-2 mb-6">
      {labels.map((label, index) => (
        <React.Fragment key={index}>
          <div className="flex items-center gap-2">
            <div
              className="w-8 h-8 rounded-full flex items-center justify-center text-sm font-bold transition-all"
              style={{
                background:
                  index < currentStep
                    ? '#2E8B57'
                    : index === currentStep
                      ? '#295be8'
                      : '#edf1f7',
                color: index <= currentStep ? '#ffffff' : '#707e92',
              }}
            >
              {index < currentStep ? <Check className="w-4 h-4" /> : index + 1}
            </div>
            <span
              className="text-xs font-medium hidden sm:block"
              style={{ color: index === currentStep ? '#192434' : '#707e92' }}
            >
              {label}
            </span>
          </div>
          {index < labels.length - 1 && (
            <div
              className="w-8 h-0.5 mx-1"
              style={{
                background: index < currentStep ? '#2E8B57' : '#edf1f7',
              }}
            />
          )}
        </React.Fragment>
      ))}
    </div>
  )
}
