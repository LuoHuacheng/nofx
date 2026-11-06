import { Check } from 'lucide-react'
import type { AIModel } from '../../types'
import { getModelIcon } from '../common/ModelIcons'
import { getShortName } from './model-constants'

interface ModelCardProps {
  model: AIModel
  selected: boolean
  onClick: () => void
  configured?: boolean
}

export function ModelCard({
  model,
  selected,
  onClick,
  configured,
}: ModelCardProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex flex-col items-center gap-2 p-4 rounded-xl transition-all hover:scale-105"
      style={{
        background: selected ? 'rgba(41,91,232, 0.12)' : '#ffffff',
        border: selected
          ? '2px solid #295be8'
          : '2px solid rgba(25,36,52,0.14)',
      }}
    >
      <div className="relative">
        <div className="w-12 h-12 rounded-xl flex items-center justify-center bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)]">
          {getModelIcon(model.provider || model.id, {
            width: 32,
            height: 32,
          }) || (
            <span className="text-lg font-bold" style={{ color: '#295be8' }}>
              {model.name[0]}
            </span>
          )}
        </div>
        {selected && (
          <div
            className="absolute -top-1 -right-1 w-5 h-5 rounded-full flex items-center justify-center"
            style={{ background: '#2E8B57' }}
          >
            <Check className="w-3 h-3 text-white" />
          </div>
        )}
        {configured && !selected && (
          <div
            className="absolute -top-1 -right-1 w-4 h-4 rounded-full flex items-center justify-center"
            style={{ background: '#295be8' }}
          >
            <Check className="w-2.5 h-2.5 text-white" />
          </div>
        )}
      </div>
      <span className="text-sm font-semibold" style={{ color: '#192434' }}>
        {getShortName(model.name)}
      </span>
      <span
        className="text-[10px] px-2 py-0.5 rounded-full uppercase tracking-wide"
        style={{ background: 'rgba(41,91,232, 0.18)', color: '#295be8' }}
      >
        {model.provider}
      </span>
    </button>
  )
}
