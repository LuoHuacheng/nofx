import { useState, useEffect } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import useSWR from 'swr'
import { api } from '../../lib/api'
import { getCachedSystemConfig, isClaw402GuideEnabled } from '../../lib/config'
import { ApiError } from '../../lib/httpClient'
import { ROUTES } from '../../router/paths'
import type {
  TraderInfo,
  CreateTraderRequest,
  AIModel,
  Exchange,
  ExchangeAccountState,
} from '../../types'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import { useAuth } from '../../contexts/AuthContext'
import { TraderConfigModal } from './TraderConfigModal'
import { ExchangeConfigModal } from './ExchangeConfigModal'
import { ModelConfigModal } from './ModelConfigModal'
import { ConfigStatusGrid } from './ConfigStatusGrid'
import { TradersList } from './TradersList'
import { AutopilotLaunchPanel } from './AutopilotLaunchPanel'
import {
  Bot,
  Plus,
  ArrowUpRight,
  Cable,
  RefreshCw,
  Settings2,
} from 'lucide-react'
import './ConfigPage.css'
import { confirmToast } from '../../lib/notify'
import { toast } from 'sonner'

interface AITradersPageProps {
  onTraderSelect?: (traderId: string) => void
}

export function AITradersPage({ onTraderSelect }: AITradersPageProps) {
  const { language } = useLanguage()
  const { user, token } = useAuth()
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const [configsLoading, setConfigsLoading] = useState(true)
  const [configError, setConfigError] = useState('')
  const [showLaunch, setShowLaunch] = useState(
    searchParams.get('setup') === 'hyperliquid-funds'
  )
  const configView =
    searchParams.get('view') === 'autopilot' ||
    searchParams.get('setup') === 'hyperliquid-funds'
      ? 'autopilot'
      : 'connections'
  const changeConfigView = (view: string) => {
    const next = new URLSearchParams(searchParams)
    if (view === 'autopilot') next.set('view', view)
    else next.delete('view')
    setSearchParams(next, { replace: true })
  }
  const [showCreateModal, setShowCreateModal] = useState(false)
  const [showEditModal, setShowEditModal] = useState(false)
  const [showModelModal, setShowModelModal] = useState(false)
  const [showExchangeModal, setShowExchangeModal] = useState(false)
  const [editingModel, setEditingModel] = useState<string | null>(null)
  const [editingExchange, setEditingExchange] = useState<string | null>(null)
  const [initialModelId, setInitialModelId] = useState<string | null>(null)
  const [initialExchangeType, setInitialExchangeType] = useState<string | null>(
    null
  )
  const [editingTrader, setEditingTrader] = useState<any>(null)
  const [allModels, setAllModels] = useState<AIModel[]>([])
  const [allExchanges, setAllExchanges] = useState<Exchange[]>([])
  const [exchangeAccountStates, setExchangeAccountStates] = useState<
    Record<string, ExchangeAccountState>
  >({})
  const [isExchangeAccountStatesLoading, setIsExchangeAccountStatesLoading] =
    useState(false)
  const [accountStateError, setAccountStateError] = useState('')
  const [supportedModels, setSupportedModels] = useState<AIModel[]>([])
  const [visibleTraderAddresses, setVisibleTraderAddresses] = useState<
    Set<string>
  >(new Set())
  const [visibleExchangeAddresses, setVisibleExchangeAddresses] = useState<
    Set<string>
  >(new Set())
  const [copiedId, setCopiedId] = useState<string | null>(null)

  const refreshAccountStates = async () => {
    setIsExchangeAccountStatesLoading(true)
    setAccountStateError('')
    try {
      const response = await api.getExchangeAccountState()
      setExchangeAccountStates(response.states || {})
    } catch {
      setExchangeAccountStates({})
      setAccountStateError(
        'Account verification could not finish. Your connection is saved. Retry the check below.'
      )
    } finally {
      setIsExchangeAccountStatesLoading(false)
    }
  }

  const loadConfigs = async () => {
    setConfigsLoading(true)
    setConfigError('')
    if (!user || !token) {
      const models = await api.getSupportedModels()
      setSupportedModels(models)
      setConfigsLoading(false)
      return
    }

    setIsExchangeAccountStatesLoading(true)
    try {
      const [modelConfigs, exchangeConfigs, models] = await Promise.all([
        api.getModelConfigs(),
        api.getExchangeConfigs(),
        api.getSupportedModels(),
        refreshAccountStates(),
      ])
      setAllModels(modelConfigs)
      setAllExchanges(exchangeConfigs)
      setSupportedModels(models)
    } catch (error) {
      setConfigError(
        'Connections could not be loaded. Saved credentials have not been changed.'
      )
      throw error
    } finally {
      setConfigsLoading(false)
      setIsExchangeAccountStatesLoading(false)
    }
  }

  // Toggle wallet address visibility for a trader
  const toggleTraderAddressVisibility = (traderId: string) => {
    setVisibleTraderAddresses((prev) => {
      const next = new Set(prev)
      if (next.has(traderId)) {
        next.delete(traderId)
      } else {
        next.add(traderId)
      }
      return next
    })
  }

  // Toggle wallet address visibility for an exchange
  const toggleExchangeAddressVisibility = (exchangeId: string) => {
    setVisibleExchangeAddresses((prev) => {
      const next = new Set(prev)
      if (next.has(exchangeId)) {
        next.delete(exchangeId)
      } else {
        next.add(exchangeId)
      }
      return next
    })
  }

  // Copy wallet address to clipboard
  const handleCopyAddress = async (id: string, address: string) => {
    try {
      await navigator.clipboard.writeText(address)
      setCopiedId(id)
      setTimeout(() => setCopiedId(null), 2000)
    } catch (err) {
      console.error('Failed to copy address:', err)
    }
  }

  const {
    data: traders,
    mutate: mutateTraders,
    isLoading: isTradersLoading,
  } = useSWR<TraderInfo[]>(user && token ? 'traders' : null, api.getTraders, {
    refreshInterval: 5000,
  })

  useEffect(() => {
    void loadConfigs().catch(() => {})
  }, [user, token])

  const configuredModels =
    allModels?.filter((m) => {
      return (
        m.enabled ||
        m.has_api_key ||
        (m.customApiUrl && m.customApiUrl.trim() !== '')
      )
    }) || []

  const configuredExchanges =
    allExchanges?.filter((e) => {
      if (e.id === 'aster') {
        return e.asterUser && e.asterUser.trim() !== ''
      }
      if (e.id === 'hyperliquid') {
        return e.hyperliquidWalletAddr && e.hyperliquidWalletAddr.trim() !== ''
      }
      return (
        e.enabled ||
        e.has_api_key ||
        e.has_hyperliquid_secret ||
        e.has_aster_private_key ||
        e.has_lighter_private_key ||
        e.has_lighter_api_key_private_key
      )
    }) || []

  const enabledModels = allModels?.filter((m) => m.enabled) || []
  const enabledExchanges =
    allExchanges?.filter((e) => {
      if (!e.enabled) return false
      if (e.id === 'aster') {
        return (
          e.asterUser &&
          e.asterUser.trim() !== '' &&
          e.asterSigner &&
          e.asterSigner.trim() !== ''
        )
      }
      if (e.id === 'hyperliquid') {
        return e.hyperliquidWalletAddr && e.hyperliquidWalletAddr.trim() !== ''
      }
      return true
    }) || []

  const getModelUsageInfo = (modelId: string) => {
    const usingTraders = traders?.filter((tr) => tr.ai_model === modelId) || []
    const runningCount = usingTraders.filter((tr) => tr.is_running).length
    const totalCount = usingTraders.length
    return { runningCount, totalCount, usingTraders }
  }

  const getExchangeUsageInfo = (exchangeId: string) => {
    const usingTraders =
      traders?.filter((tr) => tr.exchange_id === exchangeId) || []
    const runningCount = usingTraders.filter((tr) => tr.is_running).length
    const totalCount = usingTraders.length
    return { runningCount, totalCount, usingTraders }
  }

  const isModelUsedByAnyTrader = (modelId: string) => {
    return traders?.some((tr) => tr.ai_model === modelId) || false
  }

  const isExchangeUsedByAnyTrader = (exchangeId: string) => {
    return traders?.some((tr) => tr.exchange_id === exchangeId) || false
  }

  const getTradersUsingModel = (modelId: string) => {
    return traders?.filter((tr) => tr.ai_model === modelId) || []
  }

  const getTradersUsingExchange = (exchangeId: string) => {
    return traders?.filter((tr) => tr.exchange_id === exchangeId) || []
  }

  const handleCreateTrader = async (data: CreateTraderRequest) => {
    try {
      const model = allModels?.find((m) => m.id === data.ai_model_id)
      const exchange = allExchanges?.find((e) => e.id === data.exchange_id)

      if (!model?.enabled) {
        toast.error(t('modelNotConfigured', language))
        return
      }

      if (!exchange?.enabled) {
        toast.error(t('exchangeNotConfigured', language))
        return
      }

      await api.createTrader(data)
      toast.success(t('aiTradersToast.created', language))
      setShowCreateModal(false)
      await mutateTraders()
    } catch (error) {
      console.error('Failed to create trader:', error)
      toast.error(t('createTraderFailed', language))
    }
  }

  const handleEditTrader = async (traderId: string) => {
    try {
      const traderConfig = await api.getTraderConfig(traderId)
      setEditingTrader(traderConfig)
      setShowEditModal(true)
    } catch (error) {
      console.error('Failed to fetch trader config:', error)
      toast.error(t('getTraderConfigFailed', language))
    }
  }

  const handleSaveEditTrader = async (data: CreateTraderRequest) => {
    if (!editingTrader) return

    try {
      const model = enabledModels?.find((m) => m.id === data.ai_model_id)
      const exchange = enabledExchanges?.find((e) => e.id === data.exchange_id)

      if (!model) {
        toast.error(t('modelConfigNotExist', language))
        return
      }

      if (!exchange) {
        toast.error(t('exchangeConfigNotExist', language))
        return
      }

      const request = {
        name: data.name,
        ai_model_id: data.ai_model_id,
        exchange_id: data.exchange_id,
        strategy_id: data.strategy_id,
        scan_interval_minutes: data.scan_interval_minutes,
        is_cross_margin: data.is_cross_margin,
        show_in_competition: data.show_in_competition,
      }

      await api.updateTrader(editingTrader.trader_id, request)
      toast.success(t('aiTradersToast.saved', language))
      setShowEditModal(false)
      setEditingTrader(null)
      await mutateTraders()
    } catch (error) {
      console.error('Failed to update trader:', error)
      toast.error(t('updateTraderFailed', language))
    }
  }

  const handleDeleteTrader = async (traderId: string) => {
    {
      const ok = await confirmToast(t('confirmDeleteTrader', language))
      if (!ok) return
    }

    try {
      await api.deleteTrader(traderId)
      toast.success(t('aiTradersToast.deleted', language))

      await mutateTraders()
    } catch (error) {
      console.error('Failed to delete trader:', error)
      toast.error(t('deleteTraderFailed', language))
    }
  }

  const handleToggleTrader = async (traderId: string, running: boolean) => {
    try {
      if (running) {
        await api.stopTrader(traderId)
        toast.success(t('aiTradersToast.stopped', language))
      } else {
        await api.startTrader(traderId)
        toast.success(t('aiTradersToast.started', language))
      }

      await mutateTraders()
    } catch (error) {
      // Launch preflight rejections carry an actionable reason (e.g. "AI fee
      // wallet is empty") — show it instead of a generic failure toast.
      if (
        error instanceof ApiError &&
        error.errorKey === 'trader.start.preflight_failed'
      ) {
        toast.error(error.message)
        return
      }
      toast.error(t('operationFailed', language))
    }
  }

  const handleToggleCompetition = async (
    traderId: string,
    currentShowInCompetition: boolean
  ) => {
    try {
      const newValue = !currentShowInCompetition
      await api.toggleCompetition(traderId, newValue)
      toast.success(
        newValue
          ? t('aiTradersToast.showInCompetition', language)
          : t('aiTradersToast.hideInCompetition', language)
      )

      await mutateTraders()
    } catch (error) {
      console.error('Failed to toggle competition visibility:', error)
      toast.error(t('operationFailed', language))
    }
  }

  const handleModelClick = (modelId: string) => {
    setInitialModelId(null)
    setEditingModel(modelId)
    setShowModelModal(true)
  }

  const handleExchangeClick = (exchangeId: string) => {
    setInitialExchangeType(null)
    setEditingExchange(exchangeId)
    setShowExchangeModal(true)
  }

  const handleDeleteConfig = async <T extends { id: string }>(config: {
    id: string
    type: 'model' | 'exchange'
    checkInUse: (id: string) => boolean
    getUsingTraders: (id: string) => any[]
    cannotDeleteKey: string
    confirmDeleteKey: string
    allItems: T[] | undefined
    clearFields: (item: T) => T
    buildRequest: (items: T[]) => any
    updateApi: (request: any) => Promise<void>
    refreshApi: () => Promise<T[]>
    setItems: (items: T[]) => void
    closeModal: () => void
    errorKey: string
  }) => {
    if (config.checkInUse(config.id)) {
      const usingTraders = config.getUsingTraders(config.id)
      const traderNames = usingTraders.map((tr) => tr.trader_name).join(', ')
      toast.error(
        `${t(config.cannotDeleteKey, language)} · ${t('tradersUsing', language)}: ${traderNames} · ${t('pleaseDeleteTradersFirst', language)}`
      )
      return
    }

    {
      const ok = await confirmToast(t(config.confirmDeleteKey, language))
      if (!ok) return
    }

    try {
      const updatedItems =
        config.allItems?.map((item) =>
          item.id === config.id ? config.clearFields(item) : item
        ) || []

      const request = config.buildRequest(updatedItems)
      await config.updateApi(request)
      toast.success(t('aiTradersToast.configUpdated', language))

      const refreshedItems = await config.refreshApi()
      config.setItems(refreshedItems)

      config.closeModal()
    } catch (error) {
      console.error(`Failed to delete ${config.type} config:`, error)
      toast.error(t(config.errorKey, language))
    }
  }

  const handleDeleteModelConfig = async (modelId: string) => {
    await handleDeleteConfig({
      id: modelId,
      type: 'model',
      checkInUse: isModelUsedByAnyTrader,
      getUsingTraders: getTradersUsingModel,
      cannotDeleteKey: 'cannotDeleteModelInUse',
      confirmDeleteKey: 'confirmDeleteModel',
      allItems: allModels,
      clearFields: (m) => ({
        ...m,
        apiKey: '',
        customApiUrl: '',
        customModelName: '',
        enabled: false,
      }),
      buildRequest: (models) => ({
        models: Object.fromEntries(
          models.map((model) => [
            model.provider,
            {
              enabled: model.enabled,
              api_key: model.apiKey || '',
              custom_api_url: model.customApiUrl || '',
              custom_model_name: model.customModelName || '',
            },
          ])
        ),
      }),
      updateApi: api.updateModelConfigs,
      refreshApi: api.getModelConfigs,
      setItems: (items) => {
        setAllModels([...items])
      },
      closeModal: () => {
        setShowModelModal(false)
        setEditingModel(null)
      },
      errorKey: 'deleteConfigFailed',
    })
  }

  const handleSaveModelConfig = async (
    modelId: string,
    apiKey: string,
    customApiUrl?: string,
    customModelName?: string
  ) => {
    try {
      const existingModel = allModels?.find((m) => m.id === modelId)
      let updatedModels

      const modelToUpdate =
        existingModel || supportedModels?.find((m) => m.id === modelId)
      if (!modelToUpdate) {
        toast.error(t('modelNotExist', language))
        return
      }

      if (existingModel) {
        updatedModels =
          allModels?.map((m) =>
            m.id === modelId
              ? {
                  ...m,
                  apiKey,
                  customApiUrl: customApiUrl || '',
                  customModelName: customModelName || '',
                  enabled: true,
                }
              : m
          ) || []
      } else {
        const newModel = {
          ...modelToUpdate,
          apiKey,
          customApiUrl: customApiUrl || '',
          customModelName: customModelName || '',
          enabled: true,
        }
        updatedModels = [...(allModels || []), newModel]
      }

      const request = {
        models: Object.fromEntries(
          updatedModels.map((model) => [
            model.provider,
            {
              enabled: model.enabled,
              api_key: model.apiKey || '',
              custom_api_url: model.customApiUrl || '',
              custom_model_name: model.customModelName || '',
            },
          ])
        ),
      }

      await api.updateModelConfigs(request)
      toast.success(t('aiTradersToast.modelConfigUpdated', language))

      const refreshedModels = await api.getModelConfigs()
      setAllModels(refreshedModels)

      setShowModelModal(false)
      setEditingModel(null)
    } catch (error) {
      toast.error(t('saveConfigFailed', language))
      throw new Error(
        'Could not save the AI connection. Your wallet backup remains valid.'
      )
    }
  }

  const handleDeleteExchangeConfig = async (exchangeId: string) => {
    if (isExchangeUsedByAnyTrader(exchangeId)) {
      const tradersUsing = getTradersUsingExchange(exchangeId)
      toast.error(
        `${t('cannotDeleteExchangeInUse', language)}: ${tradersUsing.join(', ')}`
      )
      return
    }

    const ok = await confirmToast(t('confirmDeleteExchange', language))
    if (!ok) return

    try {
      await api.deleteExchange(exchangeId)
      toast.success(t('aiTradersToast.exchangeDeleted', language))

      const refreshedExchanges = await api.getExchangeConfigs()
      setAllExchanges(refreshedExchanges)

      setShowExchangeModal(false)
      setEditingExchange(null)
    } catch (error) {
      console.error('Failed to delete exchange config:', error)
      toast.error(t('deleteExchangeConfigFailed', language))
    }
  }

  const handleSaveExchangeConfig = async (
    exchangeId: string | null,
    exchangeType: string,
    accountName: string,
    apiKey: string,
    secretKey?: string,
    passphrase?: string,
    testnet?: boolean,
    hyperliquidWalletAddr?: string,
    asterUser?: string,
    asterSigner?: string,
    asterPrivateKey?: string,
    lighterWalletAddr?: string,
    lighterPrivateKey?: string,
    lighterApiKeyPrivateKey?: string,
    lighterApiKeyIndex?: number
  ) => {
    try {
      if (exchangeId) {
        const existingExchange = allExchanges?.find((e) => e.id === exchangeId)
        if (!existingExchange) {
          toast.error(t('exchangeNotExist', language))
          return
        }

        const request = {
          exchanges: {
            [exchangeId]: {
              enabled: true,
              api_key: apiKey || '',
              secret_key: secretKey || '',
              passphrase: passphrase || '',
              testnet: testnet || false,
              hyperliquid_wallet_addr: hyperliquidWalletAddr || '',
              hyperliquid_unified_account: exchangeType === 'hyperliquid',
              aster_user: asterUser || '',
              aster_signer: asterSigner || '',
              aster_private_key: asterPrivateKey || '',
              lighter_wallet_addr: lighterWalletAddr || '',
              lighter_private_key: lighterPrivateKey || '',
              lighter_api_key_private_key: lighterApiKeyPrivateKey || '',
              lighter_api_key_index: lighterApiKeyIndex || 0,
            },
          },
        }

        await api.updateExchangeConfigsEncrypted(request)
        toast.success(t('aiTradersToast.exchangeConfigUpdated', language))
      } else {
        const createRequest = {
          exchange_type: exchangeType,
          account_name: accountName,
          enabled: true,
          api_key: apiKey || '',
          secret_key: secretKey || '',
          passphrase: passphrase || '',
          testnet: testnet || false,
          hyperliquid_wallet_addr: hyperliquidWalletAddr || '',
          hyperliquid_unified_account: exchangeType === 'hyperliquid',
          aster_user: asterUser || '',
          aster_signer: asterSigner || '',
          aster_private_key: asterPrivateKey || '',
          lighter_wallet_addr: lighterWalletAddr || '',
          lighter_private_key: lighterPrivateKey || '',
          lighter_api_key_private_key: lighterApiKeyPrivateKey || '',
          lighter_api_key_index: lighterApiKeyIndex || 0,
        }

        await api.createExchangeEncrypted(createRequest)
        toast.success(t('aiTradersToast.exchangeCreated', language))
      }

      const refreshedExchanges = await api.getExchangeConfigs()
      setAllExchanges(refreshedExchanges)

      setShowExchangeModal(false)
      setEditingExchange(null)
      await refreshAccountStates()
    } catch (error) {
      console.error('Failed to save exchange config:', error)
      toast.error(t('saveConfigFailed', language))
    }
  }

  const handleAddModel = () => {
    setInitialModelId(null)
    setEditingModel(null)
    setShowModelModal(true)
  }

  const handleAddExchange = () => {
    setInitialExchangeType(null)
    setEditingExchange(null)
    setShowExchangeModal(true)
  }

  const handleOpenClaw402Config = () => {
    const configuredClaw402 = allModels?.find(
      (model) => model.provider === 'claw402'
    )
    const supportedClaw402 = supportedModels?.find(
      (model) => model.provider === 'claw402'
    )
    const modelId = configuredClaw402?.id || supportedClaw402?.id || 'claw402'

    setEditingModel(configuredClaw402?.id || null)
    setInitialModelId(modelId)
    setShowModelModal(true)
  }

  const handleOpenHyperliquidConfig = () => {
    const existingHyperliquid = allExchanges?.find(
      (exchange) =>
        exchange.exchange_type === 'hyperliquid' ||
        exchange.id === 'hyperliquid'
    )

    setEditingExchange(existingHyperliquid?.id || null)
    setInitialExchangeType(existingHyperliquid ? null : 'hyperliquid')
    setShowExchangeModal(true)
  }

  useEffect(() => {
    if (!user || !token) return

    const setupTarget = searchParams.get('setup')
    if (!setupTarget) return

    if (setupTarget === 'claw402') {
      // The welcome page shows the deposit QR, auto-creates the wallet if
      // needed, and polls the balance — friendlier than the key-config modal.
      if (isClaw402GuideEnabled(getCachedSystemConfig())) {
        navigate(ROUTES.welcome)
      }
    } else if (setupTarget === 'hyperliquid') {
      handleOpenHyperliquidConfig()
    } else if (setupTarget === 'model') {
      // Entry for configuring a non-Claw402 provider (e.g. from the guest
      // page when the Claw402 guide is disabled).
      handleAddModel()
    } else if (setupTarget === 'hyperliquid-funds') {
      setShowLaunch(true)
      // Funding shortfall: bring the guided launch panel (with the trading
      // balance step and live preflight polling) into view.
      document
        .getElementById('autopilot-launch-panel')
        ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      toast.info(
        'Deposit USDC to your Hyperliquid account, the balance check updates automatically.'
      )
    } else {
      return
    }

    const nextParams = new URLSearchParams(searchParams)
    if (setupTarget === 'hyperliquid-funds') nextParams.set('view', 'autopilot')
    nextParams.delete('setup')
    setSearchParams(nextParams, { replace: true })
  }, [
    allExchanges,
    allModels,
    searchParams,
    setSearchParams,
    supportedModels,
    token,
    user,
  ])

  const refreshLaunchState = async () => {
    await Promise.all([loadConfigs(), mutateTraders()])
  }

  const hasRunningTrader = (traders || []).some((trader) => trader.is_running)
  const launchPanel = (
    <AutopilotLaunchPanel
      models={allModels}
      exchanges={allExchanges}
      exchangeAccountStates={exchangeAccountStates}
      traders={traders || []}
      isLoggedIn={Boolean(user && token)}
      language={language}
      onRefresh={refreshLaunchState}
      onOpenClaw402Config={handleOpenClaw402Config}
      onOpenHyperliquidConfig={handleOpenHyperliquidConfig}
    />
  )

  return (
    <div className="config-page">
      <header className="config-page-heading">
        <div>
          <span className="config-eyebrow">YOUR WORKSPACE</span>
          <h1>Configuration</h1>
          <p>Connect your AI. Link your accounts. Choose how you trade.</p>
        </div>
        <button
          className="config-primary"
          onClick={() => navigate(ROUTES.planning)}
        >
          Open Frontier <ArrowUpRight size={15} />
        </button>
      </header>
      <div className="config-layout">
        <aside className="config-sidebar">
          <span className="config-nav-label">SETUP</span>
          <button
            aria-current={configView === 'connections' ? 'page' : undefined}
            onClick={() => changeConfigView('connections')}
          >
            <Cable size={17} />
            <span>
              Connections<small>AI & trading accounts</small>
            </span>
          </button>
          <button
            aria-current={configView === 'autopilot' ? 'page' : undefined}
            onClick={() => changeConfigView('autopilot')}
          >
            <Bot size={17} />
            <span>
              Autopilot<small>Optional automated trading</small>
            </span>
            <b>{traders?.length || 0}</b>
          </button>
          <span className="config-nav-label">PREFERENCES</span>
          <button onClick={() => navigate(ROUTES.settings)}>
            <Settings2 size={17} />
            <span>
              Account settings<small>Profile & security</small>
            </span>
          </button>
          <div className="config-sidebar-note">
            <strong>You’re in control.</strong>
            <p>
              Frontier requires approval for each trade. Autopilot runs only
              when you start it.
            </p>
          </div>
        </aside>
        <div className="config-main">
          <div className="config-content-heading">
            <div>
              <h2>
                {configView === 'connections'
                  ? 'Your connections'
                  : 'Autopilot'}
              </h2>
              <p>
                {configView === 'connections'
                  ? 'Manage the services you use across NOFX.'
                  : 'Configure and manage your automated traders.'}
              </p>
            </div>
            <button
              className="config-refresh"
              disabled={configsLoading}
              onClick={() => {
                void refreshLaunchState().catch(() => {})
              }}
            >
              <RefreshCw size={14} />{' '}
              {configsLoading ? 'Refreshing…' : 'Refresh'}
            </button>
          </div>
          {configError && (
            <p className="config-load-error" role="alert">
              {configError}
            </p>
          )}
          {configView === 'connections' && (
            <>
              {/* Configuration Status Grid */}
              {!configError && (
                <ConfigStatusGrid
                  configuredModels={configuredModels}
                  loading={configsLoading}
                  onAddModel={handleAddModel}
                  onAddExchange={handleAddExchange}
                  configuredExchanges={configuredExchanges}
                  exchangeAccountStates={exchangeAccountStates}
                  accountStateError={accountStateError}
                  onCheckAccounts={() => void refreshAccountStates()}
                  isExchangeAccountStatesLoading={
                    isExchangeAccountStatesLoading
                  }
                  visibleExchangeAddresses={visibleExchangeAddresses}
                  copiedId={copiedId}
                  language={language}
                  getModelUsageInfo={getModelUsageInfo}
                  getExchangeUsageInfo={getExchangeUsageInfo}
                  onModelClick={handleModelClick}
                  onExchangeClick={handleExchangeClick}
                  onToggleExchangeAddress={toggleExchangeAddressVisibility}
                  onCopyAddress={handleCopyAddress}
                />
              )}

              <section className="config-next">
                <span className="config-eyebrow">WHAT’S NEXT?</span>
                <h3>From connections to your first trade.</h3>
                <div>
                  <button onClick={() => navigate(ROUTES.planning)}>
                    <span>
                      <strong>Explore with Frontier</strong>
                      <small>
                        Build plans with AI. Review and approve each trade
                        yourself.
                      </small>
                    </span>
                    <ArrowUpRight size={18} />
                  </button>
                  <button onClick={() => changeConfigView('autopilot')}>
                    <span>
                      <strong>Set up Autopilot</strong>
                      <small>
                        Choose a strategy and explicitly start an automated
                        trader.
                      </small>
                    </span>
                    <ArrowUpRight size={18} />
                  </button>
                </div>
              </section>
            </>
          )}
          {configView === 'autopilot' && (
            <>
              <div className="config-autopilot-actions">
                <div>
                  <strong>
                    {isTradersLoading
                      ? 'Checking Autopilot status…'
                      : hasRunningTrader
                        ? 'Autopilot is running'
                        : 'No Autopilot is running'}
                  </strong>
                  <p>
                    {configsLoading
                      ? 'Checking saved connections…'
                      : `${configuredModels.length} AI connection${configuredModels.length === 1 ? '' : 's'} · ${configuredExchanges.length} trading account${configuredExchanges.length === 1 ? '' : 's'}`}
                  </p>
                </div>
                <button onClick={() => setShowLaunch((v) => !v)}>
                  {showLaunch ? 'Hide guided setup' : 'Guided setup'}
                </button>
                <button
                  className="config-primary"
                  onClick={() => setShowCreateModal(true)}
                  disabled={
                    configsLoading ||
                    !configuredModels.length ||
                    !configuredExchanges.length
                  }
                >
                  <Plus size={14} /> Create Autopilot
                </button>
              </div>
              {showLaunch && launchPanel}
              {/* Traders List */}
              <TradersList
                traders={traders}
                isLoading={isTradersLoading}
                allExchanges={allExchanges}
                configuredModelsCount={configuredModels.length}
                configuredExchangesCount={configuredExchanges.length}
                visibleTraderAddresses={visibleTraderAddresses}
                copiedId={copiedId}
                language={language}
                onTraderSelect={onTraderSelect}
                onNavigate={(path) => navigate(path)}
                onEditTrader={handleEditTrader}
                onToggleTrader={handleToggleTrader}
                onToggleCompetition={handleToggleCompetition}
                onDeleteTrader={handleDeleteTrader}
                onToggleTraderAddress={toggleTraderAddressVisibility}
                onCopyAddress={handleCopyAddress}
              />
            </>
          )}
        </div>
      </div>
      {/* Create Trader Modal */}
      {showCreateModal && (
        <TraderConfigModal
          isOpen={showCreateModal}
          isEditMode={false}
          availableModels={enabledModels}
          availableExchanges={enabledExchanges}
          onSave={handleCreateTrader}
          onClose={() => setShowCreateModal(false)}
        />
      )}

      {/* Edit Trader Modal */}
      {showEditModal && editingTrader && (
        <TraderConfigModal
          isOpen={showEditModal}
          isEditMode={true}
          traderData={editingTrader}
          availableModels={enabledModels}
          availableExchanges={enabledExchanges}
          onSave={handleSaveEditTrader}
          onClose={() => {
            setShowEditModal(false)
            setEditingTrader(null)
          }}
        />
      )}

      {/* Model Configuration Modal */}
      {showModelModal && (
        <ModelConfigModal
          allModels={supportedModels}
          configuredModels={allModels}
          editingModelId={editingModel}
          initialModelId={initialModelId}
          onSave={handleSaveModelConfig}
          onDelete={handleDeleteModelConfig}
          onClose={() => {
            setShowModelModal(false)
            setEditingModel(null)
            setInitialModelId(null)
          }}
          language={language}
        />
      )}

      {/* Exchange Configuration Modal */}
      {showExchangeModal && (
        <ExchangeConfigModal
          allExchanges={allExchanges}
          editingExchangeId={editingExchange}
          initialExchangeType={initialExchangeType}
          onSave={handleSaveExchangeConfig}
          onDelete={handleDeleteExchangeConfig}
          onClose={() => {
            setShowExchangeModal(false)
            setEditingExchange(null)
            setInitialExchangeType(null)
          }}
          language={language}
        />
      )}
    </div>
  )
}
