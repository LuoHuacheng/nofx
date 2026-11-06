import React, { useState, useEffect, useRef } from 'react'
import { Trash2, Brain, ExternalLink } from 'lucide-react'
import type { AIModel } from '../../types'
import type { Language } from '../../i18n/translations'
import { t } from '../../i18n/translations'
import { getModelIcon } from '../common/ModelIcons'
import { ModelStepIndicator } from './ModelStepIndicator'
import { ModelCard } from './ModelCard'
import { Claw402ConfigForm } from './Claw402ConfigForm'
import { AI_PROVIDER_CONFIG, getShortName } from './model-constants'

interface ModelConfigModalProps {
  allModels: AIModel[]
  configuredModels: AIModel[]
  editingModelId: string | null
  initialModelId?: string | null
  onSave: (
    modelId: string,
    apiKey: string,
    baseUrl?: string,
    modelName?: string
  ) => void | Promise<void>
  onDelete: (modelId: string) => void
  onClose: () => void
  language: Language
}

export function ModelConfigModal({
  allModels,
  configuredModels,
  editingModelId,
  initialModelId,
  onSave,
  onDelete,
  onClose,
  language,
}: ModelConfigModalProps) {
  const defaultModelId =
    editingModelId ||
    initialModelId ||
    allModels.find((model) => model.provider === 'claw402')?.id ||
    ''
  const [currentStep, setCurrentStep] = useState(defaultModelId ? 1 : 0)
  const [selectedModelId, setSelectedModelId] = useState(defaultModelId)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const savePending = useRef(false)
  const [apiKey, setApiKey] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [modelName, setModelName] = useState('')

  // The configured entry carries the saved details (wallet address, custom
  // model name, has_api_key); the template from supportedModels only describes
  // the provider. When editing, the configured entry must win — both can share
  // the same id (e.g. "claw402").
  const configuredModel = configuredModels?.find(
    (m) =>
      m.id === selectedModelId ||
      (m.provider === 'claw402' && selectedModelId === 'claw402')
  )
  const templateModel = allModels?.find((m) => m.id === selectedModelId)
  const selectedModel = configuredModel || templateModel
  const hasExistingKey = Boolean(
    configuredModel?.has_api_key || configuredModel?.apiKey
  )

  useEffect(() => {
    if (selectedModel && (editingModelId || hasExistingKey)) {
      setApiKey(selectedModel.apiKey || '')
      setBaseUrl(selectedModel.customApiUrl || '')
      setModelName(selectedModel.customModelName || '')
    }
  }, [editingModelId, selectedModel, hasExistingKey])

  const handleSelectModel = (modelId: string) => {
    setApiKey('')
    setBaseUrl('')
    setModelName('')
    setSelectedModelId(modelId)
    setCurrentStep(1)
  }

  const handleBack = () => {
    setApiKey('')
    setBaseUrl('')
    setModelName('')
    if (editingModelId) {
      onClose()
    } else {
      setCurrentStep(0)
      setSelectedModelId('')
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (savePending.current || !selectedModelId) return
    if (!apiKey.trim() && !hasExistingKey) return
    savePending.current = true
    setSaving(true)
    setSaveError('')
    try {
      await onSave(
        configuredModel?.id || selectedModelId,
        apiKey.trim(),
        baseUrl.trim() || undefined,
        modelName.trim() || undefined
      )
    } catch {
      setSaveError(
        'Could not save this AI connection. Check your settings and try again.'
      )
    } finally {
      savePending.current = false
      setSaving(false)
    }
  }

  const availableModels = allModels || []
  const configuredIds = new Set(configuredModels?.map((m) => m.id) || [])
  const stepLabels = [
    t('modelConfig.selectModel', language),
    t('modelConfig.configureApi', language),
  ]

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4 overflow-y-auto backdrop-blur-xs">
      <div
        className="rounded-2xl w-full max-w-208 relative my-8 shadow-2xl bg-nofx-bg-lighter"
        style={{
          maxHeight: 'calc(100vh - 4rem)',
        }}
      >
        <fieldset disabled={saving} className="min-w-0 border-0 p-0 m-0">
          {saving && (
            <p role="status" className="px-6 pt-4 text-sm">
              Saving AI connection…
            </p>
          )}
          {saveError && (
            <p role="alert" className="px-6 pt-4 text-sm text-red-600">
              {saveError}
            </p>
          )}
          {/* Header */}
          <div className="flex items-center justify-between p-6 pb-2">
            <div className="flex items-center gap-3">
              {currentStep > 0 && !editingModelId && (
                <button
                  type="button"
                  onClick={handleBack}
                  className="p-2 rounded-lg hover:bg-nofx-bg-deeper transition-colors"
                >
                  <svg
                    className="w-5 h-5"
                    style={{ color: '#707e92' }}
                    fill="none"
                    stroke="currentColor"
                    viewBox="0 0 24 24"
                  >
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M15 19l-7-7 7-7"
                    />
                  </svg>
                </button>
              )}
              <h3 className="text-xl font-bold" style={{ color: '#192434' }}>
                {editingModelId
                  ? t('editAIModel', language)
                  : t('addAIModel', language)}
              </h3>
            </div>
            <div className="flex items-center gap-2">
              {editingModelId && (
                <button
                  type="button"
                  onClick={() => onDelete(editingModelId)}
                  className="p-2 rounded-lg hover:bg-nofx-danger/20 transition-colors"
                  style={{ color: '#D6433A' }}
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              )}
              <button
                type="button"
                onClick={onClose}
                aria-label="Close AI setup"
                className="p-2 rounded-lg hover:bg-nofx-bg-deeper transition-colors"
                style={{ color: '#707e92' }}
              >
                ✕
              </button>
            </div>
          </div>

          {/* Step Indicator */}
          {!editingModelId && selectedModel?.provider !== 'claw402' && (
            <div className="px-6">
              <ModelStepIndicator
                currentStep={currentStep}
                labels={stepLabels}
              />
            </div>
          )}

          {/* Content */}
          <div
            className="px-6 pb-6 overflow-y-auto"
            style={{ maxHeight: 'calc(100vh - 16rem)' }}
          >
            {/* Step 0: Select Model */}
            {currentStep === 0 && !editingModelId && (
              <ModelSelectionStep
                availableModels={availableModels}
                configuredIds={configuredIds}
                selectedModelId={selectedModelId}
                onSelectModel={handleSelectModel}
                language={language}
              />
            )}

            {/* Step 1: Configure — Claw402 Dedicated UI */}
            {(currentStep === 1 || editingModelId) &&
              selectedModel &&
              (selectedModel.provider === 'claw402' ||
                selectedModel.id === 'claw402') && (
                <Claw402ConfigForm
                  apiKey={apiKey}
                  modelName={modelName}
                  editingModelId={editingModelId}
                  hasExistingKey={hasExistingKey}
                  walletModelID={configuredModel?.id}
                  initialWalletAddress={selectedModel.walletAddress}
                  onApiKeyChange={setApiKey}
                  onModelNameChange={setModelName}
                  onBack={handleBack}
                  onSubmit={handleSubmit}
                />
              )}

            {/* Step 1: Configure — Standard Providers (non-claw402) */}
            {(currentStep === 1 || editingModelId) &&
              selectedModel &&
              selectedModel.provider !== 'claw402' &&
              selectedModel.id !== 'claw402' && (
                <StandardProviderConfigForm
                  selectedModel={selectedModel}
                  apiKey={apiKey}
                  baseUrl={baseUrl}
                  modelName={modelName}
                  editingModelId={editingModelId}
                  onApiKeyChange={setApiKey}
                  onBaseUrlChange={setBaseUrl}
                  onModelNameChange={setModelName}
                  onBack={handleBack}
                  onSubmit={handleSubmit}
                  language={language}
                />
              )}
          </div>
        </fieldset>
      </div>
    </div>
  )
}

// --- Sub-components for ModelConfigModal ---

function ModelSelectionStep({
  availableModels,
  configuredIds,
  selectedModelId,
  onSelectModel,
  language,
}: {
  availableModels: AIModel[]
  configuredIds: Set<string>
  selectedModelId: string
  onSelectModel: (modelId: string) => void
  language: Language
}) {
  return (
    <div className="space-y-4">
      <div className="text-sm font-semibold" style={{ color: '#192434' }}>
        {t('modelConfig.chooseProvider', language)}
      </div>

      {/* Claw402 Featured Card */}
      {availableModels.some((m) => m.provider === 'claw402') && (
        <button
          type="button"
          onClick={() => {
            const claw = availableModels.find((m) => m.provider === 'claw402')
            if (claw) onSelectModel(claw.id)
          }}
          className="w-full p-5 rounded-xl text-left transition-all hover:scale-[1.01]"
          style={{
            background: 'rgba(41,91,232, 0.10)',
            border: '1.5px solid rgba(41,91,232, 0.4)',
          }}
        >
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl flex items-center justify-center overflow-hidden">
                {getModelIcon('claw402', { width: 40, height: 40 })}
              </div>
              <div>
                <div
                  className="font-bold text-base"
                  style={{ color: '#192434' }}
                >
                  Claw402
                  <a
                    href="https://claw402.ai"
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={(e) => e.stopPropagation()}
                    className="ml-1.5 text-[10px] font-normal px-1.5 py-0.5 rounded-sm"
                    style={{
                      color: '#295be8',
                      background: 'rgba(41,91,232, 0.1)',
                    }}
                  >
                    ↗ claw402.ai
                  </a>
                </div>
                <div className="text-xs mt-0.5" style={{ color: '#707e92' }}>
                  {t('modelConfig.payPerCall', language)}
                </div>
              </div>
            </div>
            <div className="flex items-center gap-2">
              {configuredIds.has(
                availableModels.find((m) => m.provider === 'claw402')?.id || ''
              ) && (
                <div
                  className="w-2 h-2 rounded-full"
                  style={{ background: '#2E8B57' }}
                />
              )}
              <div
                className="px-3 py-1.5 rounded-full text-xs font-bold"
                style={{
                  background: '#295be8',
                  color: '#fff',
                }}
              >
                {'🔥 ' + t('modelConfig.recommended', language)}
              </div>
            </div>
          </div>
          <div className="flex items-center gap-3 mt-3 ml-[52px]">
            <span
              className="text-[11px] px-2 py-0.5 rounded-full"
              style={{
                background: 'rgba(46, 139, 87, 0.1)',
                color: '#2E8B57',
                border: '1px solid rgba(46, 139, 87, 0.2)',
              }}
            >
              GPT · Claude · DeepSeek · GLM
            </span>
          </div>
        </button>
      )}

      <div className="grid grid-cols-3 sm:grid-cols-4 gap-3">
        {availableModels
          .filter(
            (m) =>
              !m.provider?.startsWith('blockrun') && m.provider !== 'claw402'
          )
          .map((model) => (
            <ModelCard
              key={model.id}
              model={model}
              selected={selectedModelId === model.id}
              onClick={() => onSelectModel(model.id)}
              configured={configuredIds.has(model.id)}
            />
          ))}
      </div>
      <div className="text-xs text-center pt-2" style={{ color: '#707e92' }}>
        {t('modelConfig.modelsConfigured', language)}
      </div>
    </div>
  )
}

function StandardProviderConfigForm({
  selectedModel,
  apiKey,
  baseUrl,
  modelName,
  editingModelId,
  onApiKeyChange,
  onBaseUrlChange,
  onModelNameChange,
  onBack,
  onSubmit,
  language,
}: {
  selectedModel: AIModel
  apiKey: string
  baseUrl: string
  modelName: string
  editingModelId: string | null
  onApiKeyChange: (value: string) => void
  onBaseUrlChange: (value: string) => void
  onModelNameChange: (value: string) => void
  onBack: () => void
  onSubmit: (e: React.FormEvent) => void
  language: Language
}) {
  return (
    <form onSubmit={onSubmit} className="space-y-5">
      {/* Selected Model Header */}
      <div
        className="p-4 rounded-xl flex items-center gap-4"
        style={{
          background: '#f6f8fc',
          border: '1px solid rgba(25,36,52,0.14)',
        }}
      >
        <div className="w-12 h-12 rounded-xl flex items-center justify-center bg-nofx-bg-deeper border border-nofx-gold/20">
          {getModelIcon(selectedModel.provider || selectedModel.id, {
            width: 32,
            height: 32,
          }) || (
            <span className="text-lg font-bold" style={{ color: '#295be8' }}>
              {selectedModel.name[0]}
            </span>
          )}
        </div>
        <div className="flex-1">
          <div className="font-semibold text-lg" style={{ color: '#192434' }}>
            {getShortName(selectedModel.name)}
          </div>
          <div className="text-xs" style={{ color: '#707e92' }}>
            {selectedModel.provider} •{' '}
            {AI_PROVIDER_CONFIG[selectedModel.provider]?.defaultModel ||
              selectedModel.id}
          </div>
        </div>
        {AI_PROVIDER_CONFIG[selectedModel.provider] && (
          <a
            href={AI_PROVIDER_CONFIG[selectedModel.provider].apiUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-2 px-4 py-2 rounded-lg transition-all hover:scale-105"
            style={{
              background: 'rgba(41,91,232, 0.1)',
              border: '1px solid rgba(41,91,232, 0.3)',
            }}
          >
            <ExternalLink className="w-4 h-4" style={{ color: '#295be8' }} />
            <span className="text-sm font-medium" style={{ color: '#295be8' }}>
              {t('modelConfig.getApiKey', language)}
            </span>
          </a>
        )}
      </div>

      {/* Kimi Warning */}
      {selectedModel.provider === 'kimi' && (
        <div
          className="p-4 rounded-xl"
          style={{
            background: 'rgba(214, 67, 58, 0.1)',
            border: '1px solid rgba(214, 67, 58, 0.3)',
          }}
        >
          <div className="flex items-start gap-2">
            <span style={{ fontSize: '16px' }}>⚠️</span>
            <div className="text-sm" style={{ color: '#D6433A' }}>
              {t('kimiApiNote', language)}
            </div>
          </div>
        </div>
      )}

      {/* API Key / Wallet Private Key */}
      {editingModelId && selectedModel && 'has_api_key' in selectedModel && (
        <div
          className="p-3 rounded-xl text-xs"
          style={{
            background: 'rgba(46, 139, 87, 0.08)',
            border: '1px solid rgba(46, 139, 87, 0.2)',
            color: '#2E8B57',
          }}
        >
          Current model key status:{' '}
          {selectedModel.has_api_key
            ? 'API Key configured'
            : 'API Key not configured'}
        </div>
      )}

      <div className="space-y-2">
        <label
          className="flex items-center gap-2 text-sm font-semibold"
          style={{ color: '#192434' }}
        >
          <svg
            className="w-4 h-4"
            style={{ color: '#295be8' }}
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z"
            />
          </svg>
          {'API Key *'}
        </label>
        <input
          type="password"
          value={apiKey}
          onChange={(e) => onApiKeyChange(e.target.value)}
          placeholder={
            editingModelId && selectedModel.has_api_key
              ? 'Saved. Re-enter to replace.'
              : t('enterAPIKey', language)
          }
          className="w-full px-4 py-3 rounded-xl"
          style={{
            background: '#f6f8fc',
            border: '1px solid rgba(25,36,52,0.14)',
            color: '#192434',
          }}
          required={!(editingModelId && selectedModel.has_api_key)}
        />
      </div>

      {/* Custom Base URL */}
      <div className="space-y-2">
        <label
          className="flex items-center gap-2 text-sm font-semibold"
          style={{ color: '#192434' }}
        >
          <svg
            className="w-4 h-4"
            style={{ color: '#295be8' }}
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"
            />
          </svg>
          {t('customBaseURL', language)}
        </label>
        <input
          type="url"
          value={baseUrl}
          onChange={(e) => onBaseUrlChange(e.target.value)}
          placeholder={t('customBaseURLPlaceholder', language)}
          className="w-full px-4 py-3 rounded-xl"
          style={{
            background: '#f6f8fc',
            border: '1px solid rgba(25,36,52,0.14)',
            color: '#192434',
          }}
        />
        <div className="text-xs" style={{ color: '#707e92' }}>
          {t('leaveBlankForDefault', language)}
        </div>
      </div>

      {/* Custom Model Name */}
      <div className="space-y-2">
        <label
          className="flex items-center gap-2 text-sm font-semibold"
          style={{ color: '#192434' }}
        >
          <svg
            className="w-4 h-4"
            style={{ color: '#295be8' }}
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M7 7h.01M7 3h5c.512 0 1.024.195 1.414.586l7 7a2 2 0 010 2.828l-7 7a2 2 0 01-2.828 0l-7-7A1.994 1.994 0 013 12V7a4 4 0 014-4z"
            />
          </svg>
          {t('customModelName', language)}
        </label>
        <input
          type="text"
          value={modelName}
          onChange={(e) => onModelNameChange(e.target.value)}
          placeholder={t('customModelNamePlaceholder', language)}
          className="w-full px-4 py-3 rounded-xl"
          style={{
            background: '#f6f8fc',
            border: '1px solid rgba(25,36,52,0.14)',
            color: '#192434',
          }}
        />
        <div className="text-xs" style={{ color: '#707e92' }}>
          {t('leaveBlankForDefaultModel', language)}
        </div>
      </div>

      {/* Info Box */}
      <div
        className="p-4 rounded-xl"
        style={{
          background: 'rgba(41,91,232, 0.08)',
          border: '1px solid rgba(41,91,232, 0.2)',
        }}
      >
        <div
          className="text-sm font-semibold mb-2 flex items-center gap-2"
          style={{ color: '#295be8' }}
        >
          <Brain className="w-4 h-4" />
          {t('information', language)}
        </div>
        <div className="text-xs space-y-1" style={{ color: '#707e92' }}>
          <div>• {t('modelConfigInfo1', language)}</div>
          <div>• {t('modelConfigInfo2', language)}</div>
          <div>• {t('modelConfigInfo3', language)}</div>
        </div>
      </div>

      {/* Buttons */}
      <div className="flex gap-3 pt-4">
        <button
          type="button"
          onClick={onBack}
          className="flex-1 px-4 py-3 rounded-xl text-sm font-semibold transition-all hover:bg-nofx-bg-deeper"
          style={{ background: '#edf1f7', color: '#707e92' }}
        >
          {editingModelId
            ? t('cancel', language)
            : t('modelConfig.back', language)}
        </button>
        <button
          type="submit"
          disabled={
            !selectedModel ||
            (!apiKey.trim() && !(editingModelId && selectedModel.has_api_key))
          }
          className="flex-1 flex items-center justify-center gap-2 px-4 py-3 rounded-xl text-sm font-bold transition-all hover:scale-[1.02] disabled:opacity-50 disabled:cursor-not-allowed"
          style={{ background: '#295be8', color: '#fff' }}
        >
          {t('saveConfig', language)}
          <svg
            className="w-4 h-4"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M14 5l7 7m0 0l-7 7m7-7H3"
            />
          </svg>
        </button>
      </div>
    </form>
  )
}
