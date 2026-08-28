export type Language = 'en' | 'zh'

export const translations = {
  en: {
    // Header
    appTitle: 'NOFX',
    subtitle: 'Multi-AI Model Trading Platform',
    aiTraders: 'AI Traders',
    details: 'Details',
    tradingPanel: 'Trading Panel',
    competition: 'Competition',
    running: 'RUNNING',
    stopped: 'STOPPED',
    adminMode: 'Admin Mode',
    logout: 'Logout',
    switchTrader: 'Switch Trader:',
    view: 'View',

    // Navigation
    realtimeNav: 'Leaderboard',
    configNav: 'Config',
    dashboardNav: 'Dashboard',
    strategyNav: 'Strategy',
    faqNav: 'FAQ',

    // Footer
    footerTitle: 'NOFX - AI Trading System',
    footerWarning: '⚠️ Trading involves risk. Use at your own discretion.',

    // Stats Cards
    totalEquity: 'Total Equity',
    availableBalance: 'Available Balance',
    totalPnL: 'Total P&L',
    positions: 'Positions',
    margin: 'Margin',
    free: 'Free',

    // Positions Table
    currentPositions: 'Current Positions',
    active: 'Active',
    symbol: 'Symbol',
    side: 'Side',
    entryPrice: 'Entry Price',
    stopLoss: 'Stop Loss',
    takeProfit: 'Take Profit',
    riskReward: 'Risk/Reward',
    markPrice: 'Mark Price',
    quantity: 'Quantity',
    positionValue: 'Position Value',
    leverage: 'Leverage',
    unrealizedPnL: 'Unrealized P&L',
    liqPrice: 'Liq. Price',
    long: 'LONG',
    short: 'SHORT',
    noPositions: 'No Positions',
    noActivePositions: 'No active trading positions',

    // Recent Decisions
    recentDecisions: 'Recent Decisions',
    lastCycles: 'Last {count} trading cycles',
    noDecisionsYet: 'No Decisions Yet',
    aiDecisionsWillAppear: 'AI trading decisions will appear here',
    cycle: 'Cycle',
    success: 'Success',
    failed: 'Failed',
    inputPrompt: 'Input Prompt',
    aiThinking: 'AI Chain of Thought',
    collapse: 'Collapse',
    expand: 'Expand',

    // Equity Chart
    accountEquityCurve: 'Account Equity Curve',
    noHistoricalData: 'No Historical Data',
    dataWillAppear: 'Equity curve will appear after running a few cycles',
    initialBalance: 'Initial Balance',
    currentEquity: 'Current Equity',
    historicalCycles: 'Historical Cycles',
    displayRange: 'Display Range',
    recent: 'Recent',
    allData: 'All Data',
    cycles: 'Cycles',

    // Comparison Chart
    comparisonMode: 'Comparison Mode',
    dataPoints: 'Data Points',
    currentGap: 'Current Gap',
    count: '{count} pts',

    // TradingView Chart
    marketChart: 'Market Chart',
    viewChart: 'Click to view chart',
    enterSymbol: 'Enter symbol...',
    popularSymbols: 'Popular Symbols',
    fullscreen: 'Fullscreen',
    exitFullscreen: 'Exit Fullscreen',

    // Competition Page
    aiCompetition: 'AI Competition',
    traders: 'traders',
    liveBattle: 'Live Battle',
    realTimeBattle: 'Real-time Battle',
    leader: 'Leader',
    leaderboard: 'Leaderboard',
    live: 'LIVE',
    realTime: 'LIVE',
    performanceComparison: 'Performance Comparison',
    realTimePnL: 'Real-time PnL %',
    realTimePnLPercent: 'Real-time PnL %',
    headToHead: 'Head-to-Head Battle',
    leadingBy: 'Leading by {gap}%',
    behindBy: 'Behind by {gap}%',
    equity: 'Equity',
    pnl: 'P&L',
    pos: 'Pos',

    // AI Traders Management
    manageAITraders: 'Manage your AI trading bots',
    aiModels: 'AI Models',
    exchanges: 'Exchanges',
    createTrader: 'Create Trader',
    modelConfiguration: 'Model Configuration',
    configured: 'Configured',
    notConfigured: 'Not Configured',
    currentTraders: 'Current Traders',
    noTraders: 'No AI Traders',
    createFirstTrader: 'Create your first AI trader to get started',
    dashboardEmptyTitle: "Let's Get Started!",
    dashboardEmptyDescription:
      'Create your first AI trader to automate your trading strategy. Connect an exchange, choose an AI model, and start trading in minutes!',
    goToTradersPage: 'Create Your First Trader',
    configureModelsFirst: 'Please configure AI models first',
    configureExchangesFirst: 'Please configure exchanges first',
    configureModelsAndExchangesFirst:
      'Please configure AI models and exchanges first',
    modelNotConfigured: 'Selected model is not configured',
    exchangeNotConfigured: 'Selected exchange is not configured',
    confirmDeleteTrader: 'Are you sure you want to delete this trader?',
    status: 'Status',
    start: 'Start',
    stop: 'Stop',
    createNewTrader: 'Create New AI Trader',
    selectAIModel: 'Select AI Model',
    selectExchange: 'Select Exchange',
    traderName: 'Trader Name',
    enterTraderName: 'Enter trader name',
    cancel: 'Cancel',
    create: 'Create',
    configureAIModels: 'Configure AI Models',
    configureExchanges: 'Configure Exchanges',
    aiScanInterval: 'AI Scan Decision Interval (minutes)',
    scanIntervalRecommend: 'Recommended: 15-30 minutes',
    useTestnet: 'Use Testnet',
    enabled: 'Enabled',
    save: 'Save',

    // TraderConfigModal - New keys for hardcoded Chinese strings
    fetchBalanceEditModeOnly: 'Only can fetch current balance in edit mode',
    balanceFetched: 'Current balance fetched',
    balanceFetchFailed: 'Failed to fetch balance',
    balanceFetchNetworkError:
      'Failed to fetch balance, please check network connection',
    saving: 'Saving...',
    saveSuccess: 'Saved successfully',
    saveFailed: 'Save failed',
    editTraderConfig: 'Edit Trader Configuration',
    selectStrategyAndConfigParams:
      'Select Strategy and Configure Basic Parameters',
    basicConfig: 'Basic Configuration',
    traderNameRequired: 'Trader Name *',
    enterTraderNamePlaceholder: 'Enter trader name',
    aiModelRequired: 'AI Model *',
    exchangeRequired: 'Exchange *',
    noExchangeAccount: "Don't have an exchange account? Click to register",
    discount: 'Discount',
    selectTradingStrategy: 'Select Trading Strategy',
    useStrategy: 'Use Strategy',
    noStrategyManual: '-- No Strategy (Manual Configuration) --',
    strategyActive: ' (Active)',
    strategyDefault: ' [Default]',
    noStrategyHint: 'No strategies yet, please create in Strategy Studio first',
    strategyDetails: 'Strategy Details',
    activating: 'Activating',
    coinSource: 'Coin Source',
    marginLimit: 'Margin Limit',
    tradingParams: 'Trading Parameters',
    marginMode: 'Margin Mode',
    crossMargin: 'Cross Margin',
    isolatedMargin: 'Isolated Margin',
    competitionDisplay: 'Show in Competition',
    show: 'Show',
    hide: 'Hide',
    hiddenInCompetition:
      'This trader will not be shown in the competition page when hidden',
    initialBalanceLabel: 'Initial Balance ($)',
    fetching: 'Fetching...',
    fetchCurrentBalance: 'Fetch Current Balance',
    balanceUpdateHint:
      'Used to manually update the initial balance baseline (e.g., after deposit/withdrawal)',
    autoFetchBalanceInfo:
      'The system will automatically fetch your account equity as the initial balance',
    fetchingBalance: 'Fetching balance...',
    editTrader: 'Save Changes',
    createTraderButton: 'Create Trader',

    // AI Model Configuration
    officialAPI: 'Official API',
    customAPI: 'Custom API',
    apiKey: 'API Key',
    customAPIURL: 'Custom API URL',
    enterAPIKey: 'Enter API Key',
    enterCustomAPIURL: 'Enter custom API endpoint URL',
    useOfficialAPI: 'Use official API service',
    useCustomAPI: 'Use custom API endpoint',

    // Exchange Configuration
    secretKey: 'Secret Key',
    privateKey: 'Private Key',
    walletAddress: 'Wallet Address',
    user: 'User',
    signer: 'Signer',
    passphrase: 'Passphrase',
    enterPrivateKey: 'Enter Private Key',
    enterWalletAddress: 'Enter Wallet Address',
    enterUser: 'Enter User',
    enterSigner: 'Enter Signer Address',
    enterSecretKey: 'Enter Secret Key',
    enterPassphrase: 'Enter Passphrase',
    hyperliquidPrivateKeyDesc:
      'Hyperliquid uses private key for trading authentication',
    hyperliquidWalletAddressDesc:
      'Wallet address corresponding to the private key',
    // Hyperliquid Agent Wallet (New Security Model)
    hyperliquidAgentWalletTitle: 'Hyperliquid Agent Wallet Configuration',
    hyperliquidAgentWalletDesc:
      'Use Agent Wallet for secure trading: Agent wallet signs transactions (balance ~0), Main wallet holds funds (never expose private key)',
    hyperliquidAgentPrivateKey: 'Agent Private Key',
    enterHyperliquidAgentPrivateKey: 'Enter Agent wallet private key',
    hyperliquidAgentPrivateKeyDesc:
      'Agent wallet private key for signing transactions (keep balance near 0 for security)',
    hyperliquidMainWalletAddress: 'Main Wallet Address',
    enterHyperliquidMainWalletAddress: 'Enter Main wallet address',
    hyperliquidMainWalletAddressDesc:
      'Main wallet address that holds your trading funds (never expose its private key)',
    // Aster API Pro Configuration
    asterApiProTitle: 'Aster API Pro Wallet Configuration',
    asterApiProDesc:
      'Use API Pro wallet for secure trading: API wallet signs transactions, main wallet holds funds (never expose main wallet private key)',
    asterUserDesc:
      'Main wallet address - The EVM wallet address you use to log in to Aster (Note: Only EVM wallets are supported)',
    asterSignerDesc:
      'API Pro wallet address (0x...) - Generate from https://www.asterdex.com/en/api-wallet',
    asterPrivateKeyDesc:
      'API Pro wallet private key - Get from https://www.asterdex.com/en/api-wallet (only used locally for signing, never transmitted)',
    asterUsdtWarning:
      'Important: Aster only tracks USDT balance. Please ensure you use USDT as margin currency to avoid P&L calculation errors caused by price fluctuations of other assets (BNB, ETH, etc.)',
    asterUserLabel: 'Main Wallet Address',
    asterSignerLabel: 'API Pro Wallet Address',
    asterPrivateKeyLabel: 'API Pro Wallet Private Key',
    enterAsterUser: 'Enter main wallet address (0x...)',
    enterAsterSigner: 'Enter API Pro wallet address (0x...)',
    enterAsterPrivateKey: 'Enter API Pro wallet private key',

    // LIGHTER Configuration
    lighterWalletAddress: 'L1 Wallet Address',
    lighterPrivateKey: 'L1 Private Key',
    lighterApiKeyPrivateKey: 'API Key Private Key',
    enterLighterWalletAddress: 'Enter Ethereum wallet address (0x...)',
    enterLighterPrivateKey: 'Enter L1 private key (32 bytes)',
    enterLighterApiKeyPrivateKey:
      'Enter API Key private key (40 bytes, optional)',
    lighterWalletAddressDesc:
      'Your Ethereum wallet address for account identification',
    lighterPrivateKeyDesc:
      'L1 private key for account identification (32-byte ECDSA key)',
    lighterApiKeyPrivateKeyDesc:
      'API Key private key for transaction signing (40-byte Poseidon2 key)',
    lighterApiKeyOptionalNote:
      'Without API Key, system will use limited V1 mode',
    lighterV1Description:
      'Basic Mode - Limited functionality, testing framework only',
    lighterV2Description:
      'Full Mode - Supports Poseidon2 signing and real trading',
    lighterPrivateKeyImported: 'LIGHTER private key imported',

    // Exchange names
    hyperliquidExchangeName: 'Hyperliquid',
    asterExchangeName: 'Aster DEX',

    // Secure input
    secureInputButton: 'Secure Input',
    secureInputReenter: 'Re-enter Securely',
    secureInputClear: 'Clear',
    secureInputHint:
      'Captured via secure two-step input. Use "Re-enter Securely" to update this value.',

    // Two Stage Key Modal
    twoStageModalTitle: 'Secure Key Input',
    twoStageModalDescription:
      'Use a two-step flow to enter your {length}-character private key safely.',
    twoStageStage1Title: 'Step 1 · Enter the first half',
    twoStageStage1Placeholder: 'First 32 characters (include 0x if present)',
    twoStageStage1Hint:
      'Continuing copies an obfuscation string to your clipboard as a diversion.',
    twoStageStage1Error: 'Please enter the first part before continuing.',
    twoStageNext: 'Next',
    twoStageProcessing: 'Processing…',
    twoStageCancel: 'Cancel',
    twoStageStage2Title: 'Step 2 · Enter the rest',
    twoStageStage2Placeholder: 'Remaining characters of your private key',
    twoStageStage2Hint:
      'Paste the obfuscation string somewhere neutral, then finish entering your key.',
    twoStageClipboardSuccess:
      'Obfuscation string copied. Paste it into any text field once before completing.',
    twoStageClipboardReminder:
      'Remember to paste the obfuscation string before submitting to avoid clipboard leaks.',
    twoStageClipboardManual:
      'Automatic copy failed. Copy the obfuscation string below manually.',
    twoStageBack: 'Back',
    twoStageSubmit: 'Confirm',
    twoStageInvalidFormat:
      'Invalid private key format. Expected {length} hexadecimal characters (optional 0x prefix).',
    testnetDescription:
      'Enable to connect to exchange test environment for simulated trading',
    securityWarning: 'Security Warning',
    saveConfiguration: 'Save Configuration',

    // Trader Configuration
    positionMode: 'Position Mode',
    crossMarginMode: 'Cross Margin',
    isolatedMarginMode: 'Isolated Margin',
    crossMarginDescription:
      'Cross margin: All positions share account balance as collateral',
    isolatedMarginDescription:
      'Isolated margin: Each position manages collateral independently, risk isolation',
    leverageConfiguration: 'Leverage Configuration',
    btcEthLeverage: 'BTC/ETH Leverage',
    altcoinLeverage: 'Altcoin Leverage',
    leverageRecommendation:
      'Recommended: BTC/ETH 5-10x, Altcoins 3-5x for risk control',
    tradingSymbols: 'Trading Symbols',
    tradingSymbolsPlaceholder:
      'Enter symbols, comma separated (e.g., BTCUSDT,ETHUSDT,SOLUSDT)',
    selectSymbols: 'Select Symbols',
    selectTradingSymbols: 'Select Trading Symbols',
    selectedSymbolsCount: 'Selected {count} symbols',
    clearSelection: 'Clear All',
    confirmSelection: 'Confirm',
    tradingSymbolsDescription:
      'Empty = use default symbols. Use USDT perps (e.g., BTCUSDT, ETHUSDT) or Hyperliquid XYZ USDC markets (e.g., TSLA-USDC)',
    btcEthLeverageValidation: 'BTC/ETH leverage must be between 1-50x',
    altcoinLeverageValidation: 'Altcoin leverage must be between 1-20x',
    invalidSymbolFormat:
      'Invalid symbol format: {symbol}, use USDT perps or SYMBOL-USDC',

    // System Prompt Templates
    systemPromptTemplate: 'System Prompt Template',
    promptTemplateDefault: 'Default Stable',
    promptTemplateAdaptive: 'Conservative Strategy',
    promptTemplateAdaptiveRelaxed: 'Aggressive Strategy',
    promptTemplateHansen: 'Hansen Strategy',
    promptTemplateNof1: 'NoF1 English Framework',
    promptTemplateTaroLong: 'Taro Long Position',
    promptDescDefault: '📊 Default Stable Strategy',
    promptDescDefaultContent:
      'Maximize Sharpe ratio, balanced risk-reward, suitable for beginners and stable long-term trading',
    promptDescAdaptive: '🛡️ Conservative Strategy (v6.0.0)',
    promptDescAdaptiveContent:
      'Strict risk control, BTC mandatory confirmation, high win rate priority, suitable for conservative traders',
    promptDescAdaptiveRelaxed: '⚡ Aggressive Strategy (v6.0.0)',
    promptDescAdaptiveRelaxedContent:
      'High-frequency trading, BTC optional confirmation, pursue trading opportunities, suitable for volatile markets',
    promptDescHansen: '🎯 Hansen Strategy',
    promptDescHansenContent:
      'Hansen custom strategy, maximize Sharpe ratio, for professional traders',
    promptDescNof1: '🌐 NoF1 English Framework',
    promptDescNof1Content:
      'Hyperliquid exchange specialist, English prompts, maximize risk-adjusted returns',
    promptDescTaroLong: '📈 Taro Long Position Strategy',
    promptDescTaroLongContent:
      'Data-driven decisions, multi-dimensional validation, continuous learning evolution, long position specialist',

    // Loading & Error
    loading: 'Loading...',

    // AI Traders Page - Additional
    inUse: 'In Use',
    noModelsConfigured: 'No configured AI models',
    noExchangesConfigured: 'No configured exchanges',
    signalSource: 'Signal Source',
    signalSourceConfig: 'Signal Source Configuration',
    ai500Description:
      'API endpoint for AI500 data provider, leave blank to disable this signal source',
    oiTopDescription:
      'API endpoint for open interest rankings, leave blank to disable this signal source',
    information: 'Information',
    signalSourceInfo1:
      '• Signal source configuration is per-user, each user can set their own URLs',
    signalSourceInfo2:
      '• When creating traders, you can choose whether to use these signal sources',
    signalSourceInfo3:
      '• Configured URLs will be used to fetch market data and trading signals',
    editAIModel: 'Edit AI Model',
    addAIModel: 'Add AI Model',
    confirmDeleteModel:
      'Are you sure you want to delete this AI model configuration?',
    cannotDeleteModelInUse:
      'Cannot delete this AI model because it is being used by traders',
    tradersUsing: 'Traders using this configuration',
    pleaseDeleteTradersFirst:
      'Please delete or reconfigure these traders first',
    selectModel: 'Select AI Model',
    pleaseSelectModel: 'Please select a model',
    customBaseURL: 'Base URL (Optional)',
    customBaseURLPlaceholder:
      'Custom API base URL, e.g.: https://api.openai.com/v1',
    leaveBlankForDefault: 'Leave blank to use default API address',
    modelConfigInfo1:
      '• For official API, only API Key is required, leave other fields blank',
    modelConfigInfo2:
      '• Custom Base URL and Model Name only needed for third-party proxies',
    modelConfigInfo3: '• API Key is encrypted and stored securely',
    defaultModel: 'Default model',
    applyApiKey: 'Apply API Key',
    kimiApiNote:
      'Kimi requires API Key from international site (moonshot.ai), China region keys are not compatible',
    leaveBlankForDefaultModel: 'Leave blank to use default model',
    customModelName: 'Model Name (Optional)',
    customModelNamePlaceholder: 'e.g.: deepseek-chat, qwen3-max, gpt-4o',
    saveConfig: 'Save Configuration',
    editExchange: 'Edit Exchange',
    addExchange: 'Add Exchange',
    confirmDeleteExchange:
      'Are you sure you want to delete this exchange configuration?',
    cannotDeleteExchangeInUse:
      'Cannot delete this exchange because it is being used by traders',
    pleaseSelectExchange: 'Please select an exchange',
    exchangeConfigWarning1:
      '• API keys will be encrypted, recommend using read-only or futures trading permissions',
    exchangeConfigWarning2:
      '• Do not grant withdrawal permissions to ensure fund security',
    exchangeConfigWarning3:
      '• After deleting configuration, related traders will not be able to trade',
    edit: 'Edit',
    viewGuide: 'View Guide',
    binanceSetupGuide: 'Binance Setup Guide',
    closeGuide: 'Close',
    whitelistIP: 'Whitelist IP',
    whitelistIPDesc: 'Binance requires adding server IP to API whitelist',
    serverIPAddresses: 'Server IP Addresses',
    copyIP: 'Copy',
    ipCopied: 'IP Copied',
    copyIPFailed: 'Failed to copy IP address. Please copy manually',
    loadingServerIP: 'Loading server IP...',

    // Error Messages
    createTraderFailed: 'Failed to create trader',
    getTraderConfigFailed: 'Failed to get trader configuration',
    modelConfigNotExist: 'Model configuration does not exist or is not enabled',
    exchangeConfigNotExist:
      'Exchange configuration does not exist or is not enabled',
    updateTraderFailed: 'Failed to update trader',
    deleteTraderFailed: 'Failed to delete trader',
    operationFailed: 'Operation failed',
    deleteConfigFailed: 'Failed to delete configuration',
    modelNotExist: 'Model does not exist',
    saveConfigFailed: 'Failed to save configuration',
    exchangeNotExist: 'Exchange does not exist',
    deleteExchangeConfigFailed: 'Failed to delete exchange configuration',
    saveSignalSourceFailed: 'Failed to save signal source configuration',
    encryptionFailed: 'Failed to encrypt sensitive data',

    // Login & Register
    login: 'Sign In',
    register: 'Sign Up',
    username: 'Username',
    email: 'Email',
    password: 'Password',
    confirmPassword: 'Confirm Password',
    usernamePlaceholder: 'your username',
    emailPlaceholder: 'your@email.com',
    passwordPlaceholder: 'Enter your password',
    confirmPasswordPlaceholder: 'Re-enter your password',
    passwordRequirements: 'Password requirements',
    passwordRuleMinLength: 'Minimum 8 characters',
    passwordRuleUppercase: 'At least 1 uppercase letter',
    passwordRuleLowercase: 'At least 1 lowercase letter',
    passwordRuleNumber: 'At least 1 number',
    passwordRuleSpecial: 'At least 1 special character (@#$%!&*?)',
    passwordRuleMatch: 'Passwords match',
    passwordNotMeetRequirements:
      'Password does not meet the security requirements',
    loginTitle: 'Sign in to your account',
    registerTitle: 'Create a new account',
    loginButton: 'Sign In',
    registerButton: 'Sign Up',
    back: 'Back',
    noAccount: "Don't have an account?",
    hasAccount: 'Already have an account?',
    registerNow: 'Sign up now',
    loginNow: 'Sign in now',
    forgotPassword: 'Forgot password?',
    forgotAccount: 'Forgot account?',
    forgotAccountConfirm:
      '⚠️ This will permanently delete EVERYTHING: users, traders, strategies, AI model API keys, exchange API keys, and your CLAW402 wallet. Export anything you need to keep (especially wallet private keys) BEFORE continuing. Re-registration will NOT restore them. Continue?',
    forgotAccountSuccess:
      'Account reset successful! You can now register a new account.',
    rememberMe: 'Remember me',
    resetPassword: 'Reset Password',
    resetPasswordTitle: 'Reset your password',
    newPassword: 'New Password',
    newPasswordPlaceholder: 'Enter new password (at least 6 characters)',
    resetPasswordButton: 'Reset Password',
    resetPasswordSuccess:
      'Password reset successful! Please login with your new password',
    resetPasswordFailed: 'Password reset failed',
    backToLogin: 'Back to Login',
    resetPasswordCliIntro:
      'For security, password recovery is no longer available from the browser. Run this command on the server where NOFX is installed:',
    resetPasswordCliSecurityNote:
      'This requires shell access to the server, which keeps your account safe even when NOFX is exposed to the internet.',
    resetAccountCliIntro:
      'To wipe everything and start over, run this command on the server where NOFX is installed:',
    copy: 'Copy',
    loginSuccess: 'Login successful',
    registrationSuccess: 'Registration successful',
    loginFailed: 'Login failed. Please check your email and password.',
    registrationFailed: 'Registration failed. Please try again.',
    sessionExpired: 'Session expired, please login again',
    invalidCredentials: 'Invalid email or password',
    weak: 'Weak',
    medium: 'Medium',
    strong: 'Strong',
    passwordStrength: 'Password strength',
    passwordStrengthHint:
      'Use at least 8 characters with mix of letters, numbers and symbols',
    passwordMismatch: 'Passwords do not match',
    emailRequired: 'Email is required',
    passwordRequired: 'Password is required',
    invalidEmail: 'Invalid email format',
    passwordTooShort: 'Password must be at least 6 characters',

    // Landing Page
    features: 'Features',
    howItWorks: 'How it Works',
    community: 'Community',
    language: 'Language',
    loggedInAs: 'Logged in as',
    exitLogin: 'Sign Out',
    signIn: 'Sign In',
    signUp: 'Sign Up',
    registrationClosed: 'Registration Closed',
    registrationClosedMessage:
      'User registration is currently disabled. Please contact the administrator for access.',

    // Hero Section
    githubStarsInDays: '2.5K+ GitHub Stars in 3 days',
    heroTitle1: 'Read the Market.',
    heroTitle2: 'Write the Trade.',
    heroDescription:
      'NOFX is the future standard for AI trading — an open, community-driven agentic trading OS. Supporting Binance, Aster DEX and other exchanges, self-hosted, multi-agent competition, let AI automatically make decisions, execute and optimize trades for you.',
    poweredBy: 'Powered by Aster DEX and Binance.',

    // Landing Page CTA
    readyToDefine: 'Ready to define the future of AI trading?',
    startWithCrypto:
      'Starting with crypto markets, expanding to TradFi. NOFX is the infrastructure of AgentFi.',
    getStartedNow: 'Get Started Now',
    viewSourceCode: 'View Source Code',

    // Features Section
    coreFeatures: 'Core Features',
    whyChooseNofx: 'Why Choose NOFX?',
    openCommunityDriven:
      'Open source, transparent, community-driven AI trading OS',
    openSourceSelfHosted: '100% Open Source & Self-Hosted',
    openSourceDesc:
      'Your framework, your rules. Non-black box, supports custom prompts and multi-models.',
    openSourceFeatures1: 'Fully open source code',
    openSourceFeatures2: 'Self-hosting deployment support',
    openSourceFeatures3: 'Custom AI prompts',
    openSourceFeatures4: 'Multi-model support (DeepSeek, Qwen)',
    multiAgentCompetition: 'Multi-Agent Intelligent Competition',
    multiAgentDesc:
      'AI strategies battle at high speed in sandbox, survival of the fittest, achieving strategy evolution.',
    multiAgentFeatures1: 'Multiple AI agents running in parallel',
    multiAgentFeatures2: 'Automatic strategy optimization',
    multiAgentFeatures3: 'Sandbox security testing',
    multiAgentFeatures4: 'Cross-market strategy porting',
    secureReliableTrading: 'Secure and Reliable Trading',
    secureDesc:
      'Enterprise-grade security, complete control over your funds and trading strategies.',
    secureFeatures1: 'Local private key management',
    secureFeatures2: 'Fine-grained API permission control',
    secureFeatures3: 'Real-time risk monitoring',
    secureFeatures4: 'Trading log auditing',

    // About Section
    aboutNofx: 'About NOFX',
    whatIsNofx: 'What is NOFX?',
    nofxNotAnotherBot:
      "NOFX is not another trading bot, but the 'Linux' of AI trading —",
    nofxDescription1:
      'a transparent, trustworthy open source OS that provides a unified',
    nofxDescription2:
      "'decision-risk-execution' layer, supporting all asset classes.",
    nofxDescription3:
      'Starting with crypto markets (24/7, high volatility perfect testing ground), future expansion to stocks, futures, forex. Core: open architecture, AI',
    nofxDescription4:
      'Darwinism (multi-agent self-competition, strategy evolution), CodeFi',
    nofxDescription5:
      'flywheel (developers get point rewards for PR contributions).',
    youFullControl: 'You 100% Control',
    fullControlDesc: 'Complete control over AI prompts and funds',
    startupMessages1: 'Starting automated trading system...',
    startupMessages2: 'API server started on port 8080',
    startupMessages3: 'Web console http://127.0.0.1:3000',

    // How It Works Section
    howToStart: 'How to Get Started with NOFX',
    fourSimpleSteps:
      'Four simple steps to start your AI automated trading journey',
    step1Title: 'Clone GitHub Repository',
    step1Desc:
      'git clone https://github.com/NoFxAiOS/nofx and switch to dev branch to test new features.',
    step2Title: 'Configure Environment',
    step2Desc:
      'Frontend setup for exchange APIs (like Binance, Hyperliquid), AI models and custom prompts.',
    step3Title: 'Deploy & Run',
    step3Desc:
      'One-click Docker deployment, start AI agents. Note: High-risk market, only test with money you can afford to lose.',
    step4Title: 'Optimize & Contribute',
    step4Desc:
      'Monitor trading, submit PRs to improve framework. Join Telegram to share strategies.',
    importantRiskWarning: 'Important Risk Warning',
    riskWarningText:
      'Dev branch is unstable, do not use funds you cannot afford to lose. NOFX is non-custodial, no official strategies. Trading involves risks, invest carefully.',

    // Community Section (testimonials are kept as-is since they are quotes)

    // Footer Section
    futureStandardAI: 'The future standard of AI trading',
    links: 'Links',
    resources: 'Resources',
    documentation: 'Documentation',
    supporters: 'Supporters',
    strategicInvestment: '(Strategic Investment)',

    // Login Modal
    accessNofxPlatform: 'Access NOFX Platform',
    loginRegisterPrompt:
      'Please login or register to access the full AI trading platform',
    registerNewAccount: 'Register New Account',

    // Candidate Coins Warnings
    candidateCoins: 'Candidate Coins',
    candidateCoinsZeroWarning: 'Candidate Coins Count is 0',
    possibleReasons: 'Possible Reasons:',
    ai500ApiNotConfigured:
      'AI500 data provider API not configured or inaccessible (check signal source settings)',
    apiConnectionTimeout: 'API connection timeout or returned empty data',
    noCustomCoinsAndApiFailed:
      'No custom coins configured and API fetch failed',
    solutions: 'Solutions:',
    setCustomCoinsInConfig: 'Set custom coin list in trader configuration',
    orConfigureCorrectApiUrl: 'Or configure correct data provider API address',
    orDisableAI500Options:
      'Or disable "Use AI500 Data Provider" and "Use OI Top" options',
    signalSourceNotConfigured: 'Signal Source Not Configured',
    signalSourceWarningMessage:
      'You have traders that enabled "Use AI500 Data Provider" or "Use OI Top", but signal source API address is not configured yet. This will cause candidate coins count to be 0, and traders cannot work properly.',
    configureSignalSourceNow: 'Configure Signal Source Now',

    // FAQ Page

    // FAQ Categories

    // ===== GETTING STARTED =====

    // ===== INSTALLATION =====

    // ===== CONFIGURATION =====

    // ===== TRADING =====

    // ===== TECHNICAL ISSUES =====

    // ===== SECURITY =====

    // ===== FEATURES =====

    // ===== AI MODELS =====

    // ===== CONTRIBUTING =====

    // Web Crypto Environment Check
    environmentCheck: {
      button: 'Check Secure Environment',
      checking: 'Checking...',
      description:
        'Automatically verifying whether this browser context allows Web Crypto before entering sensitive keys.',
      secureTitle: 'Secure context detected',
      secureDesc:
        'Web Crypto API is available. You can continue entering secrets with encryption enabled.',
      insecureTitle: 'Insecure context detected',
      insecureDesc:
        'This page is not running over HTTPS or a trusted localhost origin, so browsers block Web Crypto calls.',
      tipsTitle: 'How to fix:',
      tipHTTPS:
        'Serve the dashboard over HTTPS with a valid certificate (IP origins also need TLS).',
      tipLocalhost:
        'During development, open the app via http://localhost or 127.0.0.1.',
      tipIframe:
        'Avoid embedding the app in insecure HTTP iframes or reverse proxies that strip HTTPS.',
      unsupportedTitle: 'Browser does not expose Web Crypto',
      unsupportedDesc:
        'Open NOFX over HTTPS (or http://localhost during development) and avoid insecure iframes/reverse proxies so the browser can enable Web Crypto.',
      summary: 'Current origin: {origin} • Protocol: {protocol}',
      disabledTitle: 'Transport encryption disabled',
      disabledDesc:
        'Server-side transport encryption is disabled. API keys will be transmitted in plaintext. Enable TRANSPORT_ENCRYPTION=true for enhanced security.',
    },

    environmentSteps: {
      checkTitle: '1. Environment check',
      selectTitle: '2. Select exchange',
    },

    // Two-Stage Key Modal
    twoStageKey: {
      title: 'Two-Stage Private Key Input',
      stage1Description:
        'Enter the first {length} characters of your private key',
      stage2Description:
        'Enter the remaining {length} characters of your private key',
      stage1InputLabel: 'First Part',
      stage2InputLabel: 'Second Part',
      characters: 'characters',
      processing: 'Processing...',
      nextButton: 'Next',
      cancelButton: 'Cancel',
      backButton: 'Back',
      encryptButton: 'Encrypt & Submit',
      obfuscationCopied: 'Obfuscation data copied to clipboard',
      obfuscationInstruction:
        'Paste something else to clear clipboard, then continue',
      obfuscationManual: 'Manual obfuscation required',
    },

    // Error Messages
    errors: {
      privatekeyIncomplete: 'Please enter at least {expected} characters',
      privatekeyInvalidFormat:
        'Invalid private key format (should be 64 hex characters)',
      privatekeyObfuscationFailed: 'Clipboard obfuscation failed',
    },

    // Position History
    positionHistory: {
      title: 'Position History',
      loading: 'Loading position history...',
      noHistory: 'No Position History',
      noHistoryDesc: 'Closed positions will appear here after trading.',
      showingPositions: 'Showing {count} of {total} positions',
      totalPnL: 'Total P&L',
      // Stats
      totalTrades: 'Total Trades',
      winLoss: 'Win: {win} / Loss: {loss}',
      winRate: 'Win Rate',
      profitFactor: 'Profit Factor',
      profitFactorDesc: 'Total Profit / Total Loss',
      plRatio: 'P/L Ratio',
      plRatioDesc: 'Avg Win / Avg Loss',
      sharpeRatio: 'Sharpe Ratio',
      sharpeRatioDesc: 'Risk-adjusted Return',
      maxDrawdown: 'Max Drawdown',
      avgWin: 'Avg Win',
      avgLoss: 'Avg Loss',
      netPnL: 'Net P&L',
      netPnLDesc: 'After Fees',
      fee: 'Fee',
      // Direction Stats
      trades: 'Trades',
      avgPnL: 'Avg P&L',
      // Symbol Performance
      symbolPerformance: 'Symbol Performance',
      // Filters
      symbol: 'Symbol',
      allSymbols: 'All Symbols',
      side: 'Side',
      all: 'All',
      sort: 'Sort',
      latestFirst: 'Latest First',
      oldestFirst: 'Oldest First',
      highestPnL: 'Highest P&L',
      lowestPnL: 'Lowest P&L',
      // Table Headers
      entry: 'Entry',
      exit: 'Exit',
      qty: 'Qty',
      value: 'Value',
      lev: 'Lev',
      pnl: 'P&L',
      duration: 'Duration',
      closedAt: 'Closed At',
    },

    // Data Page
    dataCenter: 'Data Center',

    // Strategy Market Page
    strategyMarket: {
      title: 'STRATEGY MARKET',
      subtitle: 'GLOBAL STRATEGY DATABASE',
      description:
        'Discover, analyze, and clone high-performance trading algorithms',
      search: 'SEARCH PARAMETERS...',
      all: 'ALL PROTOCOLS',
      popular: 'TRENDING',
      recent: 'LATEST',
      myStrategies: 'MY LIBRARY',
      noStrategies: 'NO SIGNAL',
      noStrategiesDesc: 'No strategic signals detected in this frequency',
      author: 'OPERATOR',
      createdAt: 'TIMESTAMP',
      viewConfig: 'DECRYPT CONFIG',
      hideConfig: 'ENCRYPT',
      copyConfig: 'CLONE CONFIG',
      copied: 'COPIED',
      configHidden: 'ENCRYPTED',
      configHiddenDesc: 'Configuration parameters encrypted',
      indicators: 'INDICATORS',
      maxPositions: 'POS_LIMIT',
      maxLeverage: 'LEV_MAX',
      shareYours: 'UPLOAD_STRATEGY',
      makePublic: 'PUBLISH',
      loading: 'INITIALIZING...',
    },

    // Strategy Studio Page
    strategyStudio: {
      title: 'Strategy Studio',
      subtitle: 'Configure and test trading strategies',
      strategies: 'Strategies',
      newStrategy: 'New',
      strategyType: 'Strategy Type',
      aiTrading: 'AI Trading',
      aiTradingDesc: 'AI analyzes market and makes trading decisions',
      gridTrading: 'AI Grid Trading',
      gridTradingDesc: 'AI-controlled grid strategy for ranging markets',
      gridConfig: 'Grid Configuration',
      coinSource: 'Coin Source',
      indicators: 'Indicators',
      riskControl: 'Risk Control',
      promptSections: 'Prompt Editor',
      customPrompt: 'Extra Prompt',
      save: 'Save',
      saving: 'Saving...',
      activate: 'Activate',
      active: 'Active',
      default: 'Default',
      promptPreview: 'Prompt Preview',
      aiTestRun: 'AI Test',
      systemPrompt: 'System Prompt',
      userPrompt: 'User Prompt',
      loadPrompt: 'Generate Prompt',
      refreshPrompt: 'Refresh',
      promptVariant: 'Style',
      balanced: 'Balanced',
      aggressive: 'Aggressive',
      conservative: 'Conservative',
      selectModel: 'Select AI Model',
      runTest: 'Run AI Test',
      running: 'Running...',
      aiOutput: 'AI Output',
      reasoning: 'Reasoning',
      decisions: 'Decisions',
      duration: 'Duration',
      noModel: 'Please configure AI model first',
      testNote: 'Test with real AI, no trading',
      publishSettings: 'Publish',
      newStrategyName: 'New Strategy',
      strategyCopy: 'Strategy Copy',
      strategyDeleted: 'Strategy deleted',
      cannotDeleteActiveStrategy: 'Active strategy cannot be deleted',
      confirmDeleteStrategy: 'Delete this strategy?',
      confirmDelete: 'Confirm Delete',
      delete: 'Delete',
      cancel: 'Cancel',
      strategyExported: 'Strategy exported',
      invalidStrategyFile: 'Invalid strategy file',
      imported: 'Imported',
      strategyImported: 'Strategy imported',
      strategySaved: 'Strategy saved',
      importStrategy: 'Import Strategy',
      newStrategyTooltip: 'New Strategy',
      export: 'Export',
      duplicate: 'Duplicate',
      deleteTooltip: 'Delete',
      public: 'Public',
      addDescription: 'Add strategy description...',
      unsaved: 'Unsaved',
      discardChanges: 'Discard',
      selectOrCreate: 'Select or create a strategy',
      customPromptDesc:
        'Extra prompt appended to System Prompt for personalized trading style',
      customPromptPlaceholder: 'Enter custom prompt...',
      generatePromptPreview: 'Click to generate prompt preview',
      runAiTestHint: 'Click to run AI test',
      tokenEstimate: 'Token Estimate',
      tokenExceedWarning:
        'Token estimate exceeds 128K. AI requests may fail for some models.',
      tokenEstimating: 'Estimating...',
      tokenTooltip: 'Based on 200K context',
    },

    // Metric Tooltip
    metricTooltip: {
      formula: 'Formula',
    },

    // Login Required Overlay
    loginRequired: {
      title: 'SYSTEM ACCESS DENIED',
      accessDenied: 'ACCESS DENIED',
      subtitleWithFeature:
        'Module "{featureName}" requires elevated privileges',
      subtitleDefault: 'Authorization required for this module',
      description:
        'Initialize authentication protocol to unlock full system capabilities: AI Trader configuration and Strategy Market data streams.',
      benefit1: 'AI Trader Control',
      benefit2: 'HFT Strategy Market',
      benefit4: 'Full System Visualization',
      loginButton: 'EXECUTE LOGIN',
      registerButton: 'REGISTER NEW ID',
      abort: 'ABORT',
    },

    // Advanced Chart
    advancedChart: {
      updating: 'Updating...',
      indicators: 'Indicators',
      orderMarkers: 'Order Markers',
      technicalIndicators: 'Technical Indicators',
      clickToToggle: 'Click to toggle indicators',
      shares: 'shares',
      units: 'units',
    },

    // Chart With Orders
    chartWithOrders: {
      failedToLoad: 'Failed to load chart data',
      loading: 'Loading...',
      buy: 'BUY',
      sell: 'SELL',
    },

    // Comparison Chart
    comparisonChart: {
      '1d': '1D',
      '3d': '3D',
      '7d': '7D',
      '30d': '30D',
      all: 'All',
    },

    // TraderDashboardPage
    traderDashboard: {
      connectionFailed: 'Connection Failed',
      connectionFailedDesc: 'Please check if the backend service is running.',
      retry: 'Retry',
      confirmClosePosition:
        'Are you sure you want to close {symbol} {side} position?',
      confirmClose: 'Confirm Close',
      confirm: 'Confirm',
      cancel: 'Cancel',
      positionClosed: 'Position closed successfully',
      closeFailed: 'Failed to close position',
      closeAll: 'Close All',
      confirmCloseAllPositions: 'Market-close ALL {count} open positions?',
      allPositionsClosed: 'All positions closed',
      closeAllPartial: '{failed} of {count} positions failed to close',
      hideAddress: 'Hide address',
      showFullAddress: 'Show full address',
      copyAddress: 'Copy address',
      noAddressConfigured: 'No address configured',
      action: 'Action',
      entry: 'Entry',
      mark: 'Mark',
      qty: 'Qty',
      value: 'Value',
      lev: 'Lev.',
      uPnL: 'uPnL',
      liq: 'Liq.',
      closePosition: 'Close Position',
      close: 'Close',
      showingPositions: 'Showing {shown} of {total} positions',
      perPage: 'Per page',
      accountFetchFailed:
        'DATA_FETCH::FAILED — Account data unavailable, check connection',
      positionsFetchFailed: 'Position data unavailable',
      decisionsFetchFailed: 'Decision data unavailable',
    },

    // AITradersPage toast messages
    aiTradersToast: {
      creating: 'Creating...',
      created: 'Created successfully',
      createFailed: 'Creation failed',
      saving: 'Saving...',
      saved: 'Saved successfully',
      saveFailed: 'Save failed',
      deleting: 'Deleting...',
      deleted: 'Deleted successfully',
      deleteFailed: 'Deletion failed',
      stopping: 'Stopping...',
      stopped: 'Stopped',
      stopFailed: 'Stop failed',
      starting: 'Starting...',
      started: 'Started',
      startFailed: 'Start failed',
      updating: 'Updating...',
      updatingConfig: 'Updating config...',
      configUpdated: 'Config updated',
      configUpdateFailed: 'Config update failed',
      showInCompetition: 'Shown in competition',
      hideInCompetition: 'Hidden from competition',
      updateFailed: 'Update failed',
      updatingModelConfig: 'Updating model config...',
      modelConfigUpdated: 'Model config updated',
      modelConfigUpdateFailed: 'Model config update failed',
      deletingExchange: 'Deleting exchange account...',
      exchangeDeleted: 'Exchange account deleted',
      exchangeDeleteFailed: 'Failed to delete exchange account',
      updatingExchangeConfig: 'Updating exchange config...',
      exchangeConfigUpdated: 'Exchange config updated',
      exchangeConfigUpdateFailed: 'Failed to update exchange config',
      creatingExchange: 'Creating exchange account...',
      exchangeCreated: 'Exchange account created',
      exchangeCreateFailed: 'Failed to create exchange account',
    },

    // ModelConfigModal
    modelConfig: {
      selectModel: 'Select Model',
      configure: 'Configure',
      configureApi: 'Configure API',
      configureWallet: 'Configure Wallet',
      chooseProvider: 'Choose Your AI Provider',
      claw402EntryDesc:
        'Recommended default path. Use Base USDC pay-per-call instead of managing API keys.',
      otherApiEntry: 'Other API Providers',
      otherApiEntryDesc:
        'Use your own API key for OpenAI, Claude, Gemini, DeepSeek, and more.',
      payPerCall: 'Pay-as-you-go USDC · All AI Models · No API Key',
      recommended: 'Best',
      allModelsClaw: 'Pay-as-you-go with USDC — supports all major AI models',
      selectAiModel: 'Choose AI Model',
      allModelsUnified:
        'All models unified via Claw402. Switch anytime after setup.',
      setupWallet: 'Setup Wallet',
      walletInfo: 'Claw402 uses USDC on Base chain. You need an EVM wallet.',
      exportKey: 'Export private key from MetaMask, Rabby, etc.',
      dedicatedWallet:
        'Recommended: create a dedicated wallet with a small USDC balance',
      walletPrivateKey: 'Wallet Private Key (Base Chain EVM)',
      privateKeyNote:
        'Private key is only used locally for signing. Never uploaded. No ETH or gas needed.',
      howToFundUsdc: 'How to Fund USDC',
      fundStep1:
        'Withdraw USDC from exchange (Binance/OKX/Coinbase) to your wallet',
      fundStep2: 'Select Base network (very low fees)',
      fundStep3: '$5-10 USDC lasts a long time (~$0.003/call)',
      back: 'Back',
      startTrading: 'Start Trading',
      modelsConfigured: 'Models with gold badge are already configured',
      getStarted: 'Get Started',
      getApiKey: 'Get API Key',
      walletPrivateKeyLabel: 'Wallet Private Key *',
      selectModelLabel: 'Select Model',
      validating: 'Validating...',
      walletAddress: 'Wallet Address',
      usdcBalance: 'Base USDC Balance',
      claw402Connected: 'claw402 Connected',
      claw402Unreachable: 'claw402 Unreachable',
      depositUsdc: 'Deposit USDC to this address on Base chain',
      invalidKeyPrefix: 'Please add 0x at the beginning',
      invalidKeyLength: 'Should be 66 characters, currently',
      invalidKeyChars: 'Contains invalid characters',
      testConnection: 'Test Connection',
      testingConnection: 'Testing...',
    },

    // ExchangeConfigModal
    exchangeConfig: {
      selectExchange: 'Select Exchange',
      configure: 'Configure',
      chooseExchange: 'Choose Your Exchange',
      centralizedExchanges: 'Centralized Exchanges',
      decentralizedExchanges: 'Decentralized Exchanges',
      register: 'Register',
      bonus: 'Bonus',
      accountName: 'Account Name',
      accountNamePlaceholder: 'e.g., Main Account',
      pleaseEnterAccountName: 'Please enter account name',
      useBinanceFuturesApi: 'Use "Spot & Futures Trading" API',
      viewTutorial: 'View Tutorial',
      lighterApiKeySetup: 'Lighter API Key Setup',
      lighterApiKeyDesc: 'Generate an API Key on Lighter website',
      apiKeyIndex: 'API Key Index',
      apiKeyIndexTooltip: 'API Key index starts from 0',
      back: 'Back',
    },

    // TelegramConfigModal
    telegram: {
      botSetup: 'Telegram Bot Setup',
      createBot: 'Create Bot',
      bindAccount: 'Bind Account',
      done: 'Done',
      invalidTokenFormat:
        'Invalid Bot Token format. Expected "numbers:alphanumeric"',
      tokenSaved: 'Bot Token saved, waiting for binding',
      saveFailed: 'Save failed, please verify the token',
      unbound: 'Telegram account unbound',
      unbindFailed: 'Unbind failed',
      step1Title: 'Step 1: Create your Bot in Telegram',
      step1Desc1: 'Open Telegram, search for',
      step1Desc2: 'Send',
      step1Desc2Suffix: 'command',
      step1Desc3: 'Follow prompts to set bot name and username',
      step1Desc4: 'BotFather will return a Token, copy it',
      openBotFather: 'Open @BotFather',
      pasteToken: 'Paste Bot Token',
      tokenFormat: 'Format: numbers:alphanumeric, e.g. 123456789:ABCdef...',
      selectAiModel: 'Select AI Model (optional)',
      noEnabledModels: 'No enabled models. Configure one in AI Models first.',
      autoSelect: '— Auto-select (recommended)',
      autoUseEnabled: 'Leave blank to auto-use any enabled model',
      savingToken: 'Saving...',
      saveAndContinue: 'Save & Continue',
      step2Title: 'Step 2: Send /start to your Bot',
      step2Desc1: 'Search for your newly created Bot in Telegram',
      step2Desc2: 'Click Start or send',
      step2Desc3: 'Bot will automatically bind to your account',
      currentToken: 'Current Token',
      waitingForStart:
        'Waiting for you to send /start... Refresh page after sending',
      reconfigureToken: 'Reconfigure Token',
      bindSuccess: 'Bound successfully!',
      noStartReceived:
        'No /start received yet. Please send /start to your Bot first',
      checkFailed: 'Check failed',
      checkStatus: 'Check Status',
      botActive: 'Telegram Bot is Active!',
      botActiveDesc:
        'You can now control the trading system via natural language in Telegram',
      supportedCommands: 'Supported Commands',
      cmdHelp: 'Show all commands',
      cmdStatus: 'Show trader status',
      cmdNaturalLang: 'Natural language',
      cmdStartStop: 'Start/stop trader',
      cmdControl: 'Natural language control',
      cmdPositions: 'View positions',
      cmdPositionsDesc: 'Real-time position query',
      cmdStrategy: 'Configure strategy',
      cmdStrategyDesc: 'Modify trading strategy',
      unbinding: 'Unbinding...',
      unbindAccount: 'Unbind Account',
      aiModelLabel: 'AI Model (for natural language)',
      aiModelAutoSelect: '— Auto-select',
      modelUpdated: 'AI model updated',
      modelUpdateFailed: 'Update failed',
      save: 'Save',
      loading: 'Loading...',
    },

    // TraderConfigViewModal
    traderConfigView: {
      traderConfig: 'Trader Configuration',
      configInfo: '{name} configuration details',
      running: 'Running',
      stopped: 'Stopped',
      basicInfo: 'Basic Information',
      traderName: 'Trader Name',
      aiModel: 'AI Model',
      exchange: 'Exchange',
      initialBalance: 'Initial Balance',
      marginMode: 'Margin Mode',
      crossMargin: 'Cross',
      isolatedMargin: 'Isolated',
      scanInterval: '{minutes} minutes',
      scanIntervalLabel: 'Scan Interval',
      strategyUsed: 'Strategy Used',
      strategyName: 'Strategy Name',
      close: 'Close',
      yes: 'Yes',
      no: 'No',
    },
  },
  zh: {
    // Header
    appTitle: 'NOFX',
    subtitle: '多AI模型交易平台',
    aiTraders: 'AI交易员',
    details: '详情',
    tradingPanel: '交易面板',
    competition: '竞赛',
    running: '运行中',
    stopped: '已停止',
    adminMode: '管理员模式',
    logout: '退出',
    switchTrader: '切换交易员:',
    view: '查看',

    // Navigation
    realtimeNav: '排行榜',
    configNav: '配置',
    dashboardNav: '看板',
    strategyNav: '策略',
    faqNav: '常见问题',

    // Footer
    footerTitle: 'NOFX - AI交易系统',
    footerWarning: '⚠️ 交易有风险，请谨慎使用。',

    // Stats Cards
    totalEquity: '总净值',
    availableBalance: '可用余额',
    totalPnL: '总盈亏',
    positions: '持仓',
    margin: '保证金',
    free: '空闲',

    // Positions Table
    currentPositions: '当前持仓',
    active: '活跃',
    symbol: '币种',
    side: '方向',
    entryPrice: '入场价',
    stopLoss: '止损',
    takeProfit: '止盈',
    riskReward: '风险回报比',
    markPrice: '标记价',
    quantity: '数量',
    positionValue: '仓位价值',
    leverage: '杠杆',
    unrealizedPnL: '未实现盈亏',
    liqPrice: '强平价',
    long: '多头',
    short: '空头',
    noPositions: '无持仓',
    noActivePositions: '当前没有活跃的交易持仓',

    // Recent Decisions
    recentDecisions: '最近决策',
    lastCycles: '最近 {count} 个交易周期',
    noDecisionsYet: '暂无决策',
    aiDecisionsWillAppear: 'AI交易决策将显示在这里',
    cycle: '周期',
    success: '成功',
    failed: '失败',
    inputPrompt: '输入提示',
    aiThinking: '💭 AI思维链分析',
    collapse: '▼ 收起',
    expand: '▶ 展开',

    // Equity Chart
    accountEquityCurve: '账户净值曲线',
    noHistoricalData: '暂无历史数据',
    dataWillAppear: '运行几个周期后将显示收益率曲线',
    initialBalance: '初始余额',
    currentEquity: '当前净值',
    historicalCycles: '历史周期',
    displayRange: '显示范围',
    recent: '最近',
    allData: '全部数据',
    cycles: '个',

    // Comparison Chart
    comparisonMode: '对比模式',
    dataPoints: '数据点数',
    currentGap: '当前差距',
    count: '{count} 个',

    // TradingView Chart
    marketChart: '行情图表',
    viewChart: '点击查看图表',
    enterSymbol: '输入币种...',
    popularSymbols: '热门币种',
    fullscreen: '全屏',
    exitFullscreen: '退出全屏',

    // Competition Page
    aiCompetition: 'AI竞赛',
    traders: '交易员',
    liveBattle: '实时对战',
    realTimeBattle: '实时对战',
    leader: '领先者',
    leaderboard: '排行榜',
    live: '实时',
    realTime: '实时',
    performanceComparison: '表现对比',
    realTimePnL: '实时收益率',
    realTimePnLPercent: '实时收益率',
    headToHead: '正面对决',
    leadingBy: '领先 {gap}%',
    behindBy: '落后 {gap}%',
    equity: '权益',
    pnl: '收益',
    pos: '持仓',

    // AI Traders Management
    manageAITraders: '管理您的AI交易机器人',
    aiModels: 'AI模型',
    exchanges: '交易所',
    createTrader: '创建交易员',
    modelConfiguration: '模型配置',
    configured: '已配置',
    notConfigured: '未配置',
    currentTraders: '当前交易员',
    noTraders: '暂无AI交易员',
    createFirstTrader: '创建您的第一个AI交易员开始使用',
    dashboardEmptyTitle: '开始使用吧！',
    dashboardEmptyDescription:
      '创建您的第一个 AI 交易员，自动化您的交易策略。连接交易所、选择 AI 模型，几分钟内即可开始交易！',
    goToTradersPage: '创建您的第一个交易员',
    configureModelsFirst: '请先配置AI模型',
    configureExchangesFirst: '请先配置交易所',
    configureModelsAndExchangesFirst: '请先配置AI模型和交易所',
    modelNotConfigured: '所选模型未配置',
    exchangeNotConfigured: '所选交易所未配置',
    confirmDeleteTrader: '确定要删除这个交易员吗？',
    status: '状态',
    start: '启动',
    stop: '停止',
    createNewTrader: '创建新的AI交易员',
    selectAIModel: '选择AI模型',
    selectExchange: '选择交易所',
    traderName: '交易员名称',
    enterTraderName: '输入交易员名称',
    cancel: '取消',
    create: '创建',
    configureAIModels: '配置AI模型',
    configureExchanges: '配置交易所',
    aiScanInterval: 'AI 扫描决策间隔 (分钟)',
    scanIntervalRecommend: '建议: 15-30分钟',
    useTestnet: '使用测试网',
    enabled: '启用',
    save: '保存',

    // TraderConfigModal - New keys for hardcoded Chinese strings
    fetchBalanceEditModeOnly: '只有在编辑模式下才能获取当前余额',
    balanceFetched: '已获取当前余额',
    balanceFetchFailed: '获取余额失败',
    balanceFetchNetworkError: '获取余额失败，请检查网络连接',
    saving: '正在保存…',
    saveSuccess: '保存成功',
    saveFailed: '保存失败',
    editTraderConfig: '修改交易员配置',
    selectStrategyAndConfigParams: '选择策略并配置基础参数',
    basicConfig: '基础配置',
    traderNameRequired: '交易员名称 *',
    enterTraderNamePlaceholder: '请输入交易员名称',
    aiModelRequired: 'AI模型 *',
    exchangeRequired: '交易所 *',
    noExchangeAccount: '还没有交易所账号？点击注册',
    discount: '折扣优惠',
    selectTradingStrategy: '选择交易策略',
    useStrategy: '使用策略',
    noStrategyManual: '-- 不使用策略（手动配置） --',
    strategyActive: ' (当前激活)',
    strategyDefault: ' [默认]',
    noStrategyHint: '暂无策略，请先在策略工作室创建策略',
    strategyDetails: '策略详情',
    activating: '激活中',
    coinSource: '币种来源',
    marginLimit: '保证金上限',
    tradingParams: '交易参数',
    marginMode: '保证金模式',
    crossMargin: '全仓',
    isolatedMargin: '逐仓',
    competitionDisplay: '竞技场显示',
    show: '显示',
    hide: '隐藏',
    hiddenInCompetition: '隐藏后将不在竞技场页面显示此交易员',
    initialBalanceLabel: '初始余额 ($)',
    fetching: '获取中...',
    fetchCurrentBalance: '获取当前余额',
    balanceUpdateHint: '用于手动更新初始余额基准（例如充值/提现后）',
    autoFetchBalanceInfo: '系统将自动获取您的账户净值作为初始余额',
    fetchingBalance: '正在获取余额…',
    editTrader: '保存修改',
    createTraderButton: '创建交易员',

    // AI Model Configuration
    officialAPI: '官方API',
    customAPI: '自定义API',
    apiKey: 'API密钥',
    customAPIURL: '自定义API地址',
    enterAPIKey: '请输入API密钥',
    enterCustomAPIURL: '请输入自定义API端点地址',
    useOfficialAPI: '使用官方API服务',
    useCustomAPI: '使用自定义API端点',

    // Exchange Configuration
    secretKey: '密钥',
    privateKey: '私钥',
    walletAddress: '钱包地址',
    user: '用户名',
    signer: '签名者',
    passphrase: '口令',
    enterSecretKey: '输入密钥',
    enterPrivateKey: '输入私钥',
    enterWalletAddress: '输入钱包地址',
    enterUser: '输入用户名',
    enterSigner: '输入签名者地址',
    enterPassphrase: '输入Passphrase',
    hyperliquidPrivateKeyDesc: 'Hyperliquid 使用私钥进行交易认证',
    hyperliquidWalletAddressDesc: '与私钥对应的钱包地址',
    // Hyperliquid 代理钱包 (新安全模型)
    hyperliquidAgentWalletTitle: 'Hyperliquid 代理钱包配置',
    hyperliquidAgentWalletDesc:
      '使用代理钱包安全交易：代理钱包用于签名（餘額~0），主钱包持有资金（永不暴露私钥）',
    hyperliquidAgentPrivateKey: '代理私钥',
    enterHyperliquidAgentPrivateKey: '输入代理钱包私钥',
    hyperliquidAgentPrivateKeyDesc: '代理钱包仅有交易权限，无法提现',
    hyperliquidMainWalletAddress: '主钱包地址',
    enterHyperliquidMainWalletAddress: '输入主钱包地址',
    hyperliquidMainWalletAddressDesc:
      '持有交易资金的主钱包地址（永不暴露其私钥）',
    // Aster API Pro 配置
    asterApiProTitle: 'Aster API Pro 代理钱包配置',
    asterApiProDesc:
      '使用 API Pro 代理钱包安全交易：代理钱包用于签名交易，主钱包持有资金（永不暴露主钱包私钥）',
    asterUserDesc:
      '主钱包地址 - 您用于登录 Aster 的 EVM 钱包地址（仅支持 EVM 钱包）',
    asterSignerDesc:
      'API Pro 代理钱包地址 (0x...) - 从 https://www.asterdex.com/zh-CN/api-wallet 生成',
    asterPrivateKeyDesc:
      'API Pro 代理钱包私钥 - 从 https://www.asterdex.com/zh-CN/api-wallet 获取（仅在本地用于签名，不会被传输）',
    asterUsdtWarning:
      '重要提示：Aster 仅统计 USDT 余额。请确保您使用 USDT 作为保证金币种，避免其他资产（BNB、ETH等）的价格波动导致盈亏统计错误',
    asterUserLabel: '主钱包地址',
    asterSignerLabel: 'API Pro 代理钱包地址',
    asterPrivateKeyLabel: 'API Pro 代理钱包私钥',
    enterAsterUser: '输入主钱包地址 (0x...)',
    enterAsterSigner: '输入 API Pro 代理钱包地址 (0x...)',
    enterAsterPrivateKey: '输入 API Pro 代理钱包私钥',

    // LIGHTER 配置
    lighterWalletAddress: 'L1 錢包地址',
    lighterPrivateKey: 'L1 私鑰',
    lighterApiKeyPrivateKey: 'API Key 私鑰',
    enterLighterWalletAddress: '請輸入以太坊錢包地址（0x...）',
    enterLighterPrivateKey: '請輸入 L1 私鑰（32 字節）',
    enterLighterApiKeyPrivateKey: '請輸入 API Key 私鑰（40 字節，可選）',
    lighterWalletAddressDesc: '您的以太坊錢包地址，用於識別賬戶',
    lighterPrivateKeyDesc: 'L1 私鑰用於賬戶識別（32 字節 ECDSA 私鑰）',
    lighterApiKeyPrivateKeyDesc:
      'API Key 私鑰用於簽名交易（40 字節 Poseidon2 私鑰）',
    lighterApiKeyOptionalNote:
      '如果不提供 API Key，系統將使用功能受限的 V1 模式',
    lighterV1Description: '基本模式 - 功能受限，僅用於測試框架',
    lighterV2Description: '完整模式 - 支持 Poseidon2 簽名和真實交易',
    lighterPrivateKeyImported: 'LIGHTER 私鑰已導入',

    // Exchange names
    hyperliquidExchangeName: 'Hyperliquid',
    asterExchangeName: 'Aster DEX',

    // Secure input
    secureInputButton: '安全输入',
    secureInputReenter: '重新安全输入',
    secureInputClear: '清除',
    secureInputHint:
      '已通过安全双阶段输入设置。若需修改，请点击"重新安全输入"。',

    // Two Stage Key Modal
    twoStageModalTitle: '安全私钥输入',
    twoStageModalDescription: '使用双阶段流程安全输入长度为 {length} 的私钥。',
    twoStageStage1Title: '步骤一 · 输入前半段',
    twoStageStage1Placeholder: '前 32 位字符（若有 0x 前缀请保留）',
    twoStageStage1Hint:
      '继续后会将扰动字符串复制到剪贴板，用于迷惑剪贴板监控。',
    twoStageStage1Error: '请先输入第一段私钥。',
    twoStageNext: '下一步',
    twoStageProcessing: '处理中…',
    twoStageCancel: '取消',
    twoStageStage2Title: '步骤二 · 输入剩余部分',
    twoStageStage2Placeholder: '剩余的私钥字符',
    twoStageStage2Hint: '将扰动字符串粘贴到任意位置后，再完成私钥输入。',
    twoStageClipboardSuccess:
      '扰动字符串已复制。请在完成前在任意文本处粘贴一次以迷惑剪贴板记录。',
    twoStageClipboardReminder:
      '记得在提交前粘贴一次扰动字符串，降低剪贴板泄漏风险。',
    twoStageClipboardManual: '自动复制失败，请手动复制下面的扰动字符串。',
    twoStageBack: '返回',
    twoStageSubmit: '确认',
    twoStageInvalidFormat:
      '私钥格式不正确，应为 {length} 位十六进制字符（可选 0x 前缀）。',
    testnetDescription: '启用后将连接到交易所测试环境,用于模拟交易',
    securityWarning: '安全提示',
    saveConfiguration: '保存配置',

    // Trader Configuration
    positionMode: '仓位模式',
    crossMarginMode: '全仓模式',
    isolatedMarginMode: '逐仓模式',
    crossMarginDescription: '全仓模式：所有仓位共享账户余额作为保证金',
    isolatedMarginDescription: '逐仓模式：每个仓位独立管理保证金，风险隔离',
    leverageConfiguration: '杠杆配置',
    btcEthLeverage: 'BTC/ETH杠杆',
    altcoinLeverage: '山寨币杠杆',
    leverageRecommendation: '推荐：BTC/ETH 5-10倍，山寨币 3-5倍，控制风险',
    tradingSymbols: '交易币种',
    tradingSymbolsPlaceholder:
      '输入币种，逗号分隔（如：BTCUSDT,ETHUSDT,SOLUSDT）',
    selectSymbols: '选择币种',
    selectTradingSymbols: '选择交易币种',
    selectedSymbolsCount: '已选择 {count} 个币种',
    clearSelection: '清空选择',
    confirmSelection: '确认选择',
    tradingSymbolsDescription:
      '留空 = 使用默认币种。支持 USDT 合约（如：BTCUSDT, ETHUSDT）或 Hyperliquid XYZ USDC 标的（如：TSLA-USDC）',
    btcEthLeverageValidation: 'BTC/ETH杠杆必须在1-50倍之间',
    altcoinLeverageValidation: '山寨币杠杆必须在1-20倍之间',
    invalidSymbolFormat:
      '无效的币种格式：{symbol}，请使用 USDT 合约或 SYMBOL-USDC',

    // System Prompt Templates
    systemPromptTemplate: '系统提示词模板',
    promptTemplateDefault: '默认稳健',
    promptTemplateAdaptive: '保守策略',
    promptTemplateAdaptiveRelaxed: '激进策略',
    promptTemplateHansen: 'Hansen 策略',
    promptTemplateNof1: 'NoF1 英文框架',
    promptTemplateTaroLong: 'Taro 长仓',
    promptDescDefault: '📊 默认稳健策略',
    promptDescDefaultContent:
      '最大化夏普比率，平衡风险收益，适合新手和长期稳定交易',
    promptDescAdaptive: '🛡️ 保守策略 (v6.0.0)',
    promptDescAdaptiveContent:
      '严格风控，BTC 强制确认，高胜率优先，适合保守型交易者',
    promptDescAdaptiveRelaxed: '⚡ 激进策略 (v6.0.0)',
    promptDescAdaptiveRelaxedContent:
      '高频交易，BTC 可选确认，追求交易机会，适合波动市场',
    promptDescHansen: '🎯 Hansen 策略',
    promptDescHansenContent: 'Hansen 定制策略，最大化夏普比率，专业交易者专用',
    promptDescNof1: '🌐 NoF1 英文框架',
    promptDescNof1Content:
      'Hyperliquid 交易所专用，英文提示词，风险调整回报最大化',
    promptDescTaroLong: '📈 Taro 长仓策略',
    promptDescTaroLongContent:
      '数据驱动决策，多维度验证，持续学习进化，长仓专用',

    // Loading & Error
    loading: '加载中...',

    // AI Traders Page - Additional
    inUse: '正在使用',
    noModelsConfigured: '暂无已配置的AI模型',
    noExchangesConfigured: '暂无已配置的交易所',
    signalSource: '信号源',
    signalSourceConfig: '信号源配置',
    ai500Description: '用于获取 AI500 数据源的 API 地址，留空则不使用此数据源',
    oiTopDescription: '用于获取持仓量排行数据的API地址，留空则不使用此信号源',
    information: '说明',
    signalSourceInfo1:
      '• 信号源配置为用户级别，每个用户可以设置自己的信号源URL',
    signalSourceInfo2: '• 在创建交易员时可以选择是否使用这些信号源',
    signalSourceInfo3: '• 配置的URL将用于获取市场数据和交易信号',
    editAIModel: '编辑AI模型',
    addAIModel: '添加AI模型',
    confirmDeleteModel: '确定要删除此AI模型配置吗？',
    cannotDeleteModelInUse: '无法删除此AI模型，因为有交易员正在使用',
    tradersUsing: '正在使用此配置的交易员',
    pleaseDeleteTradersFirst: '请先删除或重新配置这些交易员',
    selectModel: '选择AI模型',
    pleaseSelectModel: '请选择模型',
    customBaseURL: 'Base URL (可选)',
    customBaseURLPlaceholder: '自定义API基础URL，如: https://api.openai.com/v1',
    leaveBlankForDefault: '留空则使用默认API地址',
    modelConfigInfo1: '• 使用官方 API 时，只需填写 API Key，其他字段留空即可',
    modelConfigInfo2:
      '• 自定义 Base URL 和 Model Name 仅在使用第三方代理时需要填写',
    modelConfigInfo3: '• API Key 加密存储，不会明文展示',
    defaultModel: '默认模型',
    applyApiKey: '申请 API Key',
    kimiApiNote:
      'Kimi 需要从国际站申请 API Key (moonshot.ai)，中国区 Key 不通用',
    leaveBlankForDefaultModel: '留空使用默认模型名称',
    customModelName: 'Model Name (可选)',
    customModelNamePlaceholder: '例如: deepseek-chat, qwen3-max, gpt-4o',
    saveConfig: '保存配置',
    editExchange: '编辑交易所',
    addExchange: '添加交易所',
    confirmDeleteExchange: '确定要删除此交易所配置吗？',
    cannotDeleteExchangeInUse: '无法删除此交易所，因为有交易员正在使用',
    pleaseSelectExchange: '请选择交易所',
    exchangeConfigWarning1: '• API密钥将被加密存储，建议使用只读或期货交易权限',
    exchangeConfigWarning2: '• 不要授予提现权限，确保资金安全',
    exchangeConfigWarning3: '• 删除配置后，相关交易员将无法正常交易',
    edit: '编辑',
    viewGuide: '查看教程',
    binanceSetupGuide: '币安配置教程',
    closeGuide: '关闭',
    whitelistIP: '白名单IP',
    whitelistIPDesc: '币安交易所需要填写白名单IP',
    serverIPAddresses: '服务器IP地址',
    copyIP: '复制',
    ipCopied: 'IP已复制',
    copyIPFailed: 'IP地址复制失败，请手动复制',
    loadingServerIP: '正在加载服务器IP...',

    // Error Messages
    createTraderFailed: '创建交易员失败',
    getTraderConfigFailed: '获取交易员配置失败',
    modelConfigNotExist: 'AI模型配置不存在或未启用',
    exchangeConfigNotExist: '交易所配置不存在或未启用',
    updateTraderFailed: '更新交易员失败',
    deleteTraderFailed: '删除交易员失败',
    operationFailed: '操作失败',
    deleteConfigFailed: '删除配置失败',
    modelNotExist: '模型不存在',
    saveConfigFailed: '保存配置失败',
    exchangeNotExist: '交易所不存在',
    deleteExchangeConfigFailed: '删除交易所配置失败',
    saveSignalSourceFailed: '保存信号源配置失败',
    encryptionFailed: '加密敏感数据失败',

    // Login & Register
    login: '登录',
    register: '注册',
    username: '用户名',
    email: '邮箱',
    password: '密码',
    confirmPassword: '确认密码',
    usernamePlaceholder: '请输入用户名',
    emailPlaceholder: '请输入邮箱地址',
    passwordPlaceholder: '请输入密码（至少6位）',
    confirmPasswordPlaceholder: '请再次输入密码',
    passwordRequirements: '密码要求',
    passwordRuleMinLength: '至少 8 位',
    passwordRuleUppercase: '至少 1 个大写字母',
    passwordRuleLowercase: '至少 1 个小写字母',
    passwordRuleNumber: '至少 1 个数字',
    passwordRuleSpecial: '至少 1 个特殊字符（@#$%!&*?）',
    passwordRuleMatch: '两次密码一致',
    passwordNotMeetRequirements: '密码不符合安全要求',
    loginTitle: '登录到您的账户',
    registerTitle: '创建新账户',
    loginButton: '登录',
    registerButton: '注册',
    back: '返回',
    noAccount: '还没有账户？',
    hasAccount: '已有账户？',
    registerNow: '立即注册',
    loginNow: '立即登录',
    forgotPassword: '忘记密码？',
    forgotAccount: '忘记账户？',
    forgotAccountConfirm:
      '⚠️ 这将永久删除全部数据：用户、Trader、策略、AI 模型 API Key、交易所 API Key，以及您的 CLAW402 钱包。请务必在继续前导出需要保留的内容（尤其是钱包私钥）。重新注册不会恢复任何数据。确定要继续吗？',
    forgotAccountSuccess: '账户已重置！现在可以注册新账户了。',
    rememberMe: '记住我',
    resetPassword: '重置密码',
    resetPasswordTitle: '重置您的密码',
    newPassword: '新密码',
    newPasswordPlaceholder: '请输入新密码（至少6位）',
    resetPasswordButton: '重置密码',
    resetPasswordSuccess: '密码重置成功！请使用新密码登录',
    resetPasswordFailed: '密码重置失败',
    backToLogin: '返回登录',
    resetPasswordCliIntro:
      '出于安全考虑，密码找回不再通过浏览器进行。请在部署 NOFX 的服务器上运行以下命令：',
    resetPasswordCliSecurityNote:
      '该操作需要服务器的 shell 访问权限，因此即使 NOFX 暴露在公网上，你的账户依然安全。',
    resetAccountCliIntro:
      '如需清空所有数据并重新开始，请在部署 NOFX 的服务器上运行以下命令：',
    copy: '复制',
    loginSuccess: '登录成功',
    registrationSuccess: '注册成功',
    loginFailed: '登录失败，请检查您的邮箱和密码。',
    registrationFailed: '注册失败，请重试。',
    sessionExpired: '登录已过期，请重新登录',
    invalidCredentials: '邮箱或密码错误',
    weak: '弱',
    medium: '中',
    strong: '强',
    passwordStrength: '密码强度',
    passwordStrengthHint: '建议至少8位，包含大小写、数字和符号',
    passwordMismatch: '两次输入的密码不一致',
    emailRequired: '请输入邮箱',
    passwordRequired: '请输入密码',
    invalidEmail: '邮箱格式不正确',
    passwordTooShort: '密码至少需要6个字符',

    // Landing Page
    features: '功能',
    howItWorks: '如何运作',
    community: '社区',
    language: '语言',
    loggedInAs: '已登录为',
    exitLogin: '退出登录',
    signIn: '登录',
    signUp: '注册',
    registrationClosed: '注册已关闭',
    registrationClosedMessage:
      '平台当前不开放新用户注册，如需访问请联系管理员获取账号。',

    // Hero Section
    githubStarsInDays: '3 天内 2.5K+ GitHub Stars',
    heroTitle1: 'Read the Market.',
    heroTitle2: 'Write the Trade.',
    heroDescription:
      'NOFX 是 AI 交易的未来标准——一个开放、社区驱动的代理式交易操作系统。支持 Binance、Aster DEX 等交易所，自托管、多代理竞争，让 AI 为你自动决策、执行和优化交易。',
    poweredBy: '由 Aster DEX 和 Binance 提供支持。',

    // Landing Page CTA
    readyToDefine: '准备好定义 AI 交易的未来吗？',
    startWithCrypto:
      '从加密市场起步，扩展到 TradFi。NOFX 是 AgentFi 的基础架构。',
    getStartedNow: '立即开始',
    viewSourceCode: '查看源码',

    // Features Section
    coreFeatures: '核心功能',
    whyChooseNofx: '为什么选择 NOFX？',
    openCommunityDriven: '开源、透明、社区驱动的 AI 交易操作系统',
    openSourceSelfHosted: '100% 开源与自托管',
    openSourceDesc: '你的框架，你的规则。非黑箱，支持自定义提示词和多模型。',
    openSourceFeatures1: '完全开源代码',
    openSourceFeatures2: '支持自托管部署',
    openSourceFeatures3: '自定义 AI 提示词',
    openSourceFeatures4: '多模型支持（DeepSeek、Qwen）',
    multiAgentCompetition: '多代理智能竞争',
    multiAgentDesc: 'AI 策略在沙盒中高速战斗，最优者生存，实现策略进化。',
    multiAgentFeatures1: '多 AI 代理并行运行',
    multiAgentFeatures2: '策略自动优化',
    multiAgentFeatures3: '沙盒安全测试',
    multiAgentFeatures4: '跨市场策略移植',
    secureReliableTrading: '安全可靠交易',
    secureDesc: '企业级安全保障，完全掌控你的资金和交易策略。',
    secureFeatures1: '本地私钥管理',
    secureFeatures2: 'API 权限精细控制',
    secureFeatures3: '实时风险监控',
    secureFeatures4: '交易日志审计',

    // About Section
    aboutNofx: '关于 NOFX',
    whatIsNofx: '什么是 NOFX？',
    nofxNotAnotherBot: "NOFX 不是另一个交易机器人，而是 AI 交易的 'Linux' ——",
    nofxDescription1: "一个透明、可信任的开源 OS，提供统一的 '决策-风险-执行'",
    nofxDescription2: '层，支持所有资产类别。',
    nofxDescription3:
      '从加密市场起步（24/7、高波动性完美测试场），未来扩展到股票、期货、外汇。核心：开放架构、AI',
    nofxDescription4:
      '达尔文主义（多代理自竞争、策略进化）、CodeFi 飞轮（开发者 PR',
    nofxDescription5: '贡献获积分奖励）。',
    youFullControl: '你 100% 掌控',
    fullControlDesc: '完全掌控 AI 提示词和资金',
    startupMessages1: '启动自动交易系统...',
    startupMessages2: 'API服务器启动在端口 8080',
    startupMessages3: 'Web 控制台 http://127.0.0.1:3000',

    // How It Works Section
    howToStart: '如何开始使用 NOFX',
    fourSimpleSteps: '四个简单步骤，开启 AI 自动交易之旅',
    step1Title: '拉取 GitHub 仓库',
    step1Desc:
      'git clone https://github.com/NoFxAiOS/nofx 并切换到 dev 分支测试新功能。',
    step2Title: '配置环境',
    step2Desc:
      '前端设置交易所 API（如 Binance、Hyperliquid）、AI 模型和自定义提示词。',
    step3Title: '部署与运行',
    step3Desc:
      '一键 Docker 部署，启动 AI 代理。注意：高风险市场，仅用闲钱测试。',
    step4Title: '优化与贡献',
    step4Desc: '监控交易，提交 PR 改进框架。加入 Telegram 分享策略。',
    importantRiskWarning: '重要风险提示',
    riskWarningText:
      'dev 分支不稳定，勿用无法承受损失的资金。NOFX 非托管，无官方策略。交易有风险，投资需谨慎。',

    // Community Section (testimonials are kept as-is since they are quotes)

    // Footer Section
    futureStandardAI: 'AI 交易的未来标准',
    links: '链接',
    resources: '资源',
    documentation: '文档',
    supporters: '支持方',
    strategicInvestment: '(战略投资)',

    // Login Modal
    accessNofxPlatform: '访问 NOFX 平台',
    loginRegisterPrompt: '请选择登录或注册以访问完整的 AI 交易平台',
    registerNewAccount: '注册新账号',

    // Candidate Coins Warnings
    candidateCoins: '候选币种',
    candidateCoinsZeroWarning: '候选币种数量为 0',
    possibleReasons: '可能原因：',
    ai500ApiNotConfigured:
      'AI500 数据源 API 未配置或无法访问（请检查信号源设置）',
    apiConnectionTimeout: 'API连接超时或返回数据为空',
    noCustomCoinsAndApiFailed: '未配置自定义币种且API获取失败',
    solutions: '解决方案：',
    setCustomCoinsInConfig: '在交易员配置中设置自定义币种列表',
    orConfigureCorrectApiUrl: '或者配置正确的数据源 API 地址',
    orDisableAI500Options: '或者禁用"使用 AI500 数据源"和"使用 OI Top"选项',
    signalSourceNotConfigured: '信号源未配置',
    signalSourceWarningMessage:
      '您有交易员启用了"使用 AI500 数据源"或"使用 OI Top"，但尚未配置信号源 API 地址。这将导致候选币种数量为 0，交易员无法正常工作。',
    configureSignalSourceNow: '立即配置信号源',

    // FAQ Page

    // FAQ Categories

    // ===== 入门指南 =====

    // ===== 安装部署 =====

    // ===== 配置设置 =====

    // ===== 交易相关 =====

    // ===== 技术问题 =====

    // ===== 安全相关 =====

    // ===== 功能介绍 =====

    // ===== AI 模型 =====

    // ===== 参与贡献 =====

    // Web Crypto Environment Check
    environmentCheck: {
      button: '一键检测环境',
      checking: '正在检测...',
      description: '系统将自动检测当前浏览器是否允许使用 Web Crypto。',
      secureTitle: '环境安全，已启用 Web Crypto',
      secureDesc: '页面处于安全上下文，可继续输入敏感信息并使用加密传输。',
      insecureTitle: '检测到非安全环境',
      insecureDesc:
        '当前访问未通过 HTTPS 或可信 localhost，浏览器会阻止 Web Crypto 调用。',
      tipsTitle: '修改建议：',
      tipHTTPS:
        '通过 HTTPS 访问（即使是 IP 也需证书），或部署到支持 TLS 的域名。',
      tipLocalhost: '开发阶段请使用 http://localhost 或 127.0.0.1。',
      tipIframe:
        '避免把应用嵌入在不安全的 HTTP iframe 或会降级协议的反向代理中。',
      unsupportedTitle: '浏览器未提供 Web Crypto',
      unsupportedDesc:
        '请通过 HTTPS 或本机 localhost 访问 NOFX，并避免嵌入不安全 iframe/反向代理，以符合浏览器的 Web Crypto 规则。',
      summary: '当前来源：{origin} · 协议：{protocol}',
      disabledTitle: '传输加密已禁用',
      disabledDesc:
        '服务端传输加密已关闭，API 密钥将以明文传输。如需增强安全性，请设置 TRANSPORT_ENCRYPTION=true。',
    },

    environmentSteps: {
      checkTitle: '1. 环境检测',
      selectTitle: '2. 选择交易所',
    },

    // Two-Stage Key Modal
    twoStageKey: {
      title: '两阶段私钥输入',
      stage1Description: '请输入私钥的前 {length} 位字符',
      stage2Description: '请输入私钥的后 {length} 位字符',
      stage1InputLabel: '第一部分',
      stage2InputLabel: '第二部分',
      characters: '位字符',
      processing: '处理中...',
      nextButton: '下一步',
      cancelButton: '取消',
      backButton: '返回',
      encryptButton: '加密并提交',
      obfuscationCopied: '混淆数据已复制到剪贴板',
      obfuscationInstruction: '请粘贴其他内容清空剪贴板，然后继续',
      obfuscationManual: '需要手动混淆',
    },

    // Error Messages
    errors: {
      privatekeyIncomplete: '请输入至少 {expected} 位字符',
      privatekeyInvalidFormat: '私钥格式无效（应为64位十六进制字符）',
      privatekeyObfuscationFailed: '剪贴板混淆失败',
    },

    // Position History
    positionHistory: {
      title: '历史仓位',
      loading: '加载历史仓位...',
      noHistory: '暂无历史仓位',
      noHistoryDesc: '平仓后的仓位记录将显示在此处',
      showingPositions: '显示 {count} / {total} 条记录',
      totalPnL: '总盈亏',
      // Stats
      totalTrades: '总交易次数',
      winLoss: '盈利: {win} / 亏损: {loss}',
      winRate: '胜率',
      profitFactor: '盈利因子',
      profitFactorDesc: '总盈利 / 总亏损',
      plRatio: '盈亏比',
      plRatioDesc: '平均盈利 / 平均亏损',
      sharpeRatio: '夏普比率',
      sharpeRatioDesc: '风险调整收益',
      maxDrawdown: '最大回撤',
      avgWin: '平均盈利',
      avgLoss: '平均亏损',
      netPnL: '净盈亏',
      netPnLDesc: '扣除手续费后',
      fee: '手续费',
      // Direction Stats
      trades: '交易次数',
      avgPnL: '平均盈亏',
      // Symbol Performance
      symbolPerformance: '品种表现',
      // Filters
      symbol: '交易对',
      allSymbols: '全部交易对',
      side: '方向',
      all: '全部',
      sort: '排序',
      latestFirst: '最新优先',
      oldestFirst: '最早优先',
      highestPnL: '盈利最高',
      lowestPnL: '亏损最多',
      // Table Headers
      entry: '开仓价',
      exit: '平仓价',
      qty: '数量',
      value: '仓位价值',
      lev: '杠杆',
      pnl: '盈亏',
      duration: '持仓时长',
      closedAt: '平仓时间',
    },

    // Data Page
    dataCenter: '数据中心',

    // Strategy Market Page
    strategyMarket: {
      title: '策略市场',
      subtitle: 'STRATEGY MARKETPLACE',
      description: '发现、学习并复用社区精英交易员的策略配置',
      search: '搜索参数...',
      all: '全部协议',
      popular: '热门配置',
      recent: '最新提交',
      myStrategies: '我的库',
      noStrategies: '无信号',
      noStrategiesDesc: '当前频段未检测到策略信号',
      author: 'OPERATOR',
      createdAt: 'TIMESTAMP',
      viewConfig: 'DECRYPT CONFIG',
      hideConfig: 'ENCRYPT',
      copyConfig: 'CLONE CONFIG',
      copied: 'COPIED',
      configHidden: 'ENCRYPTED',
      configHiddenDesc: '配置参数已加密',
      indicators: 'INDICATORS',
      maxPositions: 'POS_LIMIT',
      maxLeverage: 'LEV_MAX',
      shareYours: 'UPLOAD_STRATEGY',
      makePublic: 'PUBLISH',
      loading: 'INITIALIZING...',
    },

    // Strategy Studio Page
    strategyStudio: {
      title: '策略工作室',
      subtitle: '可视化配置和测试交易策略',
      strategies: '策略',
      newStrategy: '新建',
      strategyType: '策略类型',
      aiTrading: 'AI 智能交易',
      aiTradingDesc: 'AI 分析市场并自主决策买卖',
      gridTrading: 'AI 网格交易',
      gridTradingDesc: 'AI 控制网格策略，在震荡市场获利',
      gridConfig: '网格配置',
      coinSource: '币种来源',
      indicators: '技术指标',
      riskControl: '风控参数',
      promptSections: 'Prompt 编辑',
      customPrompt: '附加提示',
      save: '保存',
      saving: '保存中...',
      activate: '激活',
      active: '激活中',
      default: '默认',
      promptPreview: 'Prompt 预览',
      aiTestRun: 'AI 测试',
      systemPrompt: 'System Prompt',
      userPrompt: 'User Prompt',
      loadPrompt: '生成 Prompt',
      refreshPrompt: '刷新',
      promptVariant: '风格',
      balanced: '平衡',
      aggressive: '激进',
      conservative: '保守',
      selectModel: '选择 AI 模型',
      runTest: '运行 AI 测试',
      running: '运行中...',
      aiOutput: 'AI 输出',
      reasoning: '思维链',
      decisions: '决策',
      duration: '耗时',
      noModel: '请先配置 AI 模型',
      testNote: '使用真实 AI 模型测试，不执行交易',
      publishSettings: '发布设置',
      newStrategyName: '新策略',
      strategyCopy: '策略副本',
      strategyDeleted: '策略已删除',
      cannotDeleteActiveStrategy: '激活中的策略不能删除',
      confirmDeleteStrategy: '确定删除此策略？',
      confirmDelete: '确认删除',
      delete: '删除',
      cancel: '取消',
      strategyExported: '策略已导出',
      invalidStrategyFile: '无效的策略文件',
      imported: '导入',
      strategyImported: '策略已导入',
      strategySaved: '策略已保存',
      importStrategy: '导入策略',
      newStrategyTooltip: '新建策略',
      export: '导出',
      duplicate: '复制',
      deleteTooltip: '删除',
      public: '公开',
      addDescription: '添加策略简介...',
      unsaved: '未保存',
      discardChanges: '撤销',
      selectOrCreate: '选择或创建策略',
      customPromptDesc:
        '附加在 System Prompt 末尾的额外提示，用于补充个性化交易风格',
      customPromptPlaceholder: '输入自定义提示词...',
      generatePromptPreview: '点击生成 Prompt 预览',
      runAiTestHint: '点击运行 AI 测试',
      tokenEstimate: 'Token 预估',
      tokenExceedWarning: 'Token 估算超过 128K，部分模型请求可能失败',
      tokenEstimating: '预估中...',
      tokenTooltip: '基于 200K 上下文计算',
    },

    // Metric Tooltip
    metricTooltip: {
      formula: '计算公式',
    },

    // Login Required Overlay
    loginRequired: {
      title: '系统访问受限',
      accessDenied: '访问被拒绝',
      subtitleWithFeature: '访问「{featureName}」需要更高权限',
      subtitleDefault: '此模块需要授权访问',
      description:
        '初始化身份验证协议以解锁完整系统功能：AI 交易员配置、策略市场数据流。',
      benefit1: 'AI 交易员控制权',
      benefit2: '高频策略核心市场',
      benefit4: '全系统数据可视化',
      loginButton: '执行登录指令',
      registerButton: '注册新用户 ID',
      abort: '中止操作',
    },

    // Advanced Chart
    advancedChart: {
      updating: '更新中...',
      indicators: '指标',
      orderMarkers: '订单标记',
      technicalIndicators: '技术指标',
      clickToToggle: '点击选择需要显示的指标',
      shares: '股',
      units: '个',
    },

    // Chart With Orders
    chartWithOrders: {
      failedToLoad: '加载图表数据失败',
      loading: '加载中...',
      buy: 'BUY (买入)',
      sell: 'SELL (卖出)',
    },

    // Comparison Chart
    comparisonChart: {
      '1d': '1天',
      '3d': '3天',
      '7d': '7天',
      '30d': '30天',
      all: '全部',
    },

    traderDashboard: {
      connectionFailed: '无法连接到服务器',
      connectionFailedDesc: '请确认后端服务已启动。',
      retry: '重试',
      confirmClosePosition: '确定要平仓 {symbol} {side} 吗？',
      confirmClose: '确认平仓',
      confirm: '确认',
      cancel: '取消',
      positionClosed: '平仓成功',
      closeFailed: '平仓失败',
      closeAll: '一键全平',
      confirmCloseAllPositions: '确定要市价平掉全部 {count} 个持仓吗？',
      allPositionsClosed: '全部持仓已平',
      closeAllPartial: '{count} 个持仓中有 {failed} 个平仓失败',
      hideAddress: '隐藏地址',
      showFullAddress: '显示完整地址',
      copyAddress: '复制地址',
      noAddressConfigured: '未配置地址',
      action: '操作',
      entry: '入场价',
      mark: '标记价',
      qty: '数量',
      value: '价值',
      lev: '杠杆',
      uPnL: '未实现盈亏',
      liq: '强平价',
      closePosition: '平仓',
      close: '平仓',
      showingPositions: '显示 {shown} / {total} 个持仓',
      perPage: '每页',
      accountFetchFailed: 'DATA_FETCH::FAILED — 账户数据请求失败，请检查连接',
      positionsFetchFailed: '持仓数据请求失败',
      decisionsFetchFailed: '决策记录请求失败',
    },

    aiTradersToast: {
      creating: '正在创建…',
      created: '创建成功',
      createFailed: '创建失败',
      saving: '正在保存…',
      saved: '保存成功',
      saveFailed: '保存失败',
      deleting: '正在删除…',
      deleted: '删除成功',
      deleteFailed: '删除失败',
      stopping: '正在停止…',
      stopped: '已停止',
      stopFailed: '停止失败',
      starting: '正在启动…',
      started: '已启动',
      startFailed: '启动失败',
      updating: '正在更新…',
      updatingConfig: '正在更新配置…',
      configUpdated: '配置已更新',
      configUpdateFailed: '更新配置失败',
      showInCompetition: '已在竞技场显示',
      hideInCompetition: '已在竞技场隐藏',
      updateFailed: '更新失败',
      updatingModelConfig: '正在更新模型配置…',
      modelConfigUpdated: '模型配置已更新',
      modelConfigUpdateFailed: '更新模型配置失败',
      deletingExchange: '正在删除交易所账户…',
      exchangeDeleted: '交易所账户已删除',
      exchangeDeleteFailed: '删除交易所账户失败',
      updatingExchangeConfig: '正在更新交易所配置…',
      exchangeConfigUpdated: '交易所配置已更新',
      exchangeConfigUpdateFailed: '更新交易所配置失败',
      creatingExchange: '正在创建交易所账户…',
      exchangeCreated: '交易所账户已创建',
      exchangeCreateFailed: '创建交易所账户失败',
    },

    modelConfig: {
      selectModel: '选择模型',
      configure: '配置',
      configureApi: '配置 API',
      configureWallet: '配置钱包',
      chooseProvider: '选择 AI 模型提供商',
      claw402EntryDesc:
        '默认推荐走这条路。直接用 Base USDC 按次付费，不需要自己管理 API Key。',
      otherApiEntry: '其他 API 模型',
      otherApiEntryDesc:
        '如果你已经有自己的 OpenAI、Claude、Gemini、DeepSeek 等 API Key，再从这里进入。',
      payPerCall: 'USDC 按量付费 · 支持全部 AI 模型 · 无需 API Key',
      recommended: '推荐',
      allModelsClaw: '用 USDC 按量付费，支持所有主流 AI 模型',
      selectAiModel: '① 选择 AI 模型',
      allModelsUnified: '所有模型通过 Claw402 统一调用，创建后可随时切换',
      setupWallet: '② 设置钱包',
      walletInfo: '💡 Claw402 使用 Base 链上的 USDC 付费，你需要一个 EVM 钱包',
      exportKey: '可以用 MetaMask、Rabby 等钱包导出私钥',
      dedicatedWallet: '建议新建一个专用钱包，充入少量 USDC 即可',
      walletPrivateKey: '钱包私钥（Base 链 EVM）',
      privateKeyNote:
        '私钥仅在本地签名使用，不会上传或发送交易。无需 ETH，无 Gas 费用。',
      howToFundUsdc: '如何充值 USDC',
      fundStep1: '从交易所（Binance / OKX / Coinbase）提 USDC 到你的钱包地址',
      fundStep2: '选择 Base 网络（手续费极低）',
      fundStep3: '充入 $5-10 USDC 即可使用很长时间（约 $0.003/次调用）',
      back: '返回',
      startTrading: '开始交易',
      modelsConfigured: '带金色标记的模型已配置',
      getStarted: '开始使用',
      getApiKey: '获取 API Key',
      walletPrivateKeyLabel: '钱包私钥 *',
      selectModelLabel: '选择模型',
      validating: '验证中...',
      walletAddress: '钱包地址',
      usdcBalance: 'Base USDC 余额',
      claw402Connected: 'claw402 已连接',
      claw402Unreachable: 'claw402 不可达',
      depositUsdc: '请往此地址充值 Base 链 USDC',
      invalidKeyPrefix: '请在开头加 0x',
      invalidKeyLength: '应为 66 个字符，当前',
      invalidKeyChars: '包含非法字符',
      testConnection: '测试连接',
      testingConnection: '测试中...',
    },

    exchangeConfig: {
      selectExchange: '选择交易所',
      configure: '配置账户',
      chooseExchange: '选择您的交易所',
      centralizedExchanges: '中心化交易所 (CEX)',
      decentralizedExchanges: '去中心化交易所 (DEX)',
      register: '注册',
      bonus: '优惠',
      accountName: '账户名称',
      accountNamePlaceholder: '例如：主账户、套利账户',
      pleaseEnterAccountName: '请输入账户名称',
      useBinanceFuturesApi: '币安用户必读：使用「现货与合约交易」API',
      viewTutorial: '查看官方教程',
      lighterApiKeySetup: 'Lighter API Key 配置',
      lighterApiKeyDesc: '请在 Lighter 网站生成 API Key',
      apiKeyIndex: 'API Key 索引',
      apiKeyIndexTooltip: 'API Key 索引从0开始',
      back: '返回',
    },

    telegram: {
      botSetup: 'Telegram Bot 配置',
      createBot: '创建 Bot',
      bindAccount: '绑定账号',
      done: '完成',
      invalidTokenFormat: 'Bot Token 格式不正确，应为 "数字:字母数字串"',
      tokenSaved: 'Bot Token 已保存，等待绑定',
      saveFailed: '保存失败，请检查 Token 是否正确',
      unbound: '已解绑 Telegram 账号',
      unbindFailed: '解绑失败',
      step1Title: '第一步：在 Telegram 创建你的 Bot',
      step1Desc1: '打开 Telegram，搜索',
      step1Desc2: '发送',
      step1Desc2Suffix: '命令',
      step1Desc3: '按提示输入 Bot 名称和用户名',
      step1Desc4: 'BotFather 会返回一个 Token，复制它',
      openBotFather: '打开 @BotFather',
      pasteToken: '粘贴 Bot Token',
      tokenFormat: 'Token 格式：数字:字母数字串，如 123456789:ABCdef...',
      selectAiModel: '选择 AI 模型（可选）',
      noEnabledModels: '暂无启用的模型，请先在「AI 模型」中配置',
      autoSelect: '— 自动选择（推荐）',
      autoUseEnabled: '不选则自动使用已启用的模型',
      savingToken: '保存中...',
      saveAndContinue: '保存并继续',
      step2Title: '第二步：向你的 Bot 发送 /start',
      step2Desc1: '在 Telegram 中搜索你刚创建的 Bot',
      step2Desc2: '点击 Start 或发送',
      step2Desc3: 'Bot 会自动绑定到你的账号',
      currentToken: '当前 Token',
      waitingForStart: '⏳ 等待你发送 /start... 发送后刷新页面查看状态',
      reconfigureToken: '重新配置 Token',
      bindSuccess: '绑定成功！',
      noStartReceived: '尚未收到 /start，请先向 Bot 发送 /start',
      checkFailed: '检查失败',
      checkStatus: '检查绑定状态',
      botActive: 'Telegram Bot 已绑定！',
      botActiveDesc: '你现在可以通过 Telegram 用自然语言控制交易系统',
      supportedCommands: '支持的命令',
      cmdHelp: '查看所有命令',
      cmdStatus: '查看交易员状态',
      cmdNaturalLang: '自然语言查询',
      cmdStartStop: '启动/停止交易员',
      cmdControl: '自然语言控制',
      cmdPositions: '查看持仓',
      cmdPositionsDesc: '实时持仓查询',
      cmdStrategy: '配置策略',
      cmdStrategyDesc: '修改交易策略',
      unbinding: '解绑中...',
      unbindAccount: '解绑账号',
      aiModelLabel: 'AI 模型（用于自然语言解析）',
      aiModelAutoSelect: '— 自动选择',
      modelUpdated: 'AI 模型已更新',
      modelUpdateFailed: '更新失败',
      save: '保存',
      loading: '加载中...',
    },

    traderConfigView: {
      traderConfig: '交易员配置',
      configInfo: '{name} 的配置信息',
      running: '运行中',
      stopped: '已停止',
      basicInfo: '基础信息',
      traderName: '交易员名称',
      aiModel: 'AI模型',
      exchange: '交易所',
      initialBalance: '初始余额',
      marginMode: '保证金模式',
      crossMargin: '全仓',
      isolatedMargin: '逐仓',
      scanInterval: '{minutes} 分钟',
      scanIntervalLabel: '扫描间隔',
      strategyUsed: '使用策略',
      strategyName: '策略名称',
      close: '关闭',
      yes: '是',
      no: '否',
    },
  },
}

export function t(
  key: string,
  lang: Language,
  params?: Record<string, string | number>
): string {
  // Handle nested keys like 'twoStageKey.title'
  const keys = key.split('.')
  let value: any = translations[lang]

  for (const k of keys) {
    value = value?.[k]
  }

  let text = typeof value === 'string' ? value : key

  // Replace parameters like {count}, {gap}, etc.
  if (params) {
    Object.entries(params).forEach(([param, value]) => {
      text = text.replace(`{${param}}`, String(value))
    })
  }

  return text
}
