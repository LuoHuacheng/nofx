import {
  ArrowRight,
  ArrowUpRight,
  Layers3,
  SlidersHorizontal,
  Wallet,
  Check,
  Terminal,
} from 'lucide-react'
import { Link } from 'react-router-dom'
import { ROUTES } from '../../router/paths'
import { OFFICIAL_LINKS } from '../../constants/branding'
import { BrandLogo } from '../common/BrandLogo'

export default function FlowLanding() {
  return (
    <div className="nofx-flow-landing">
      <section className="flow-hero">
        <div className="flow-hero-copy">
          <span className="flow-eyebrow">
            <span /> Open source. Self hosted.
          </span>
          <h1>
            Intelligence
            <br />
            <em>that trades.</em>
          </h1>
          <p>An open trading system you run yourself.</p>
          <div className="flow-hero-actions">
            <Link to={ROUTES.planning} className="flow-primary">
              Open Frontier <ArrowRight size={18} />
            </Link>
            <Link to={ROUTES.traders}>
              Set up your workspace <ArrowUpRight size={16} />
            </Link>
          </div>
          <div className="flow-hero-trust">
            <span>
              <Check size={14} /> Runs on your machine
            </span>
            <span>
              <Check size={14} /> Your exchange accounts
            </span>
          </div>
        </div>
        <div
          className="flow-product-preview"
          aria-label="Illustration of the Frontier planning workspace"
        >
          <div className="flow-preview-header">
            <BrandLogo />
            <span>Frontier</span>
            <small>Workspace preview</small>
          </div>
          <div className="flow-preview-content">
            <div className="flow-preview-market">
              <span>One idea. A clear plan.</span>
              <b>Compare your entry.</b>
            </div>
            <svg
              className="flow-preview-chart"
              viewBox="0 0 600 260"
              role="img"
              aria-label="Illustrative chart showing an entry, target and stop level"
            >
              <defs>
                <linearGradient
                  id="flow-chart-fill"
                  x1="0"
                  y1="0"
                  x2="0"
                  y2="1"
                >
                  <stop stopColor="#295be8" stopOpacity=".13" />
                  <stop offset="1" stopColor="#295be8" stopOpacity="0" />
                </linearGradient>
              </defs>
              {[50, 100, 150, 200].map((y) => (
                <path key={y} d={`M0 ${y}H600`} stroke="#edf1f7" />
              ))}
              {[80, 180, 280, 380, 480].map((x) => (
                <path key={x} d={`M${x} 0V260`} stroke="#edf1f7" />
              ))}
              <path
                d="M0 214 30 200 52 210 80 185 105 190 133 156 154 169 182 128 203 137 229 98 250 119 274 108 300 145 326 129 352 142 378 100 398 115 425 77 449 94 471 59 494 81 520 66V260H0Z"
                fill="url(#flow-chart-fill)"
              />
              <path
                d="m0 214 30-14 22 10 28-25 25 5 28-34 21 13 28-41 21 9 26-39 21 21 24-11 26 37 26-16 26 13 26-42 20 15 27-38 24 17 22-35 23 22 26-15"
                fill="none"
                stroke="#295be8"
                strokeWidth="2.5"
                strokeLinejoin="round"
              />
              <rect x="350" y="48" width="174" height="87" fill="#eaf7f1" />
              <rect x="350" y="135" width="174" height="45" fill="#fdf0f0" />
              <path d="M350 48H600" stroke="#258563" strokeDasharray="4 4" />
              <text x="536" y="39" fill="#258563" fontSize="11">
                Target
              </text>
              <path d="M350 135H600" stroke="#295be8" />
              <text x="536" y="126" fill="#295be8" fontSize="11">
                Entry
              </text>
              <path d="M350 180H600" stroke="#ca565e" strokeDasharray="4 4" />
              <text x="536" y="198" fill="#ca565e" fontSize="11">
                Stop
              </text>
            </svg>
            <div className="flow-preview-plans">
              {['Market', 'Limit', 'Ladder', 'TWAP'].map((type, i) => (
                <div key={type} data-selected={i === 1}>
                  <span>0{i + 1}</span>
                  <strong>{type}</strong>
                  <small>
                    {
                      [
                        'Enter now',
                        'Choose a price',
                        'Split by price',
                        'Split by time',
                      ][i]
                    }
                  </small>
                </div>
              ))}
            </div>
            <div className="flow-preview-bottom">
              <span>
                <Check size={14} /> Review before you trade
              </span>
              <span>
                Make your next move <ArrowUpRight size={14} />
              </span>
            </div>
          </div>
        </div>
      </section>
      <section
        className="flow-exchanges"
        aria-label="Supported Frontier exchanges"
      >
        <span>Your markets. Connected.</span>
        {['Binance', 'Hyperliquid', 'OKX', 'Bitget'].map((name) => (
          <b key={name}>{name}</b>
        ))}
      </section>
      <section className="flow-features">
        <div className="flow-section-heading">
          <span className="flow-eyebrow">One connected workspace</span>
          <h2>
            Less friction.
            <br />
            More clarity.
          </h2>
          <p>AI helps you explore. Clear controls help you act.</p>
        </div>
        {[
          {
            icon: Layers3,
            title: 'See the alternatives.',
            body: 'Compare market, limit, ladder and TWAP plans. See entries, size and exits before committing.',
            label: 'Explore Frontier',
            to: ROUTES.planning,
          },
          {
            icon: Wallet,
            title: 'Know where you stand.',
            body: 'Keep trading balances, open positions and orders in view. AI fees stay separate from trading funds.',
            label: 'Connect your accounts',
            to: ROUTES.traders,
          },
          {
            icon: SlidersHorizontal,
            title: 'Make it your own.',
            body: 'Choose your AI and trading accounts. Set up optional Autopilot strategies when you’re ready.',
            label: 'Build a strategy',
            to: ROUTES.strategy,
          },
        ].map((item) => (
          <article key={item.title}>
            <item.icon size={23} />
            <h3>{item.title}</h3>
            <p>{item.body}</p>
            <Link to={item.to}>
              {item.label}
              <ArrowUpRight size={16} />
            </Link>
          </article>
        ))}
      </section>
      <section className="flow-local">
        <div>
          <span className="flow-eyebrow">
            <Terminal size={14} /> Built to run locally
          </span>
          <h2>
            Your workspace.
            <br />
            On your terms.
          </h2>
          <p>
            Install NOFX on your computer, connect your accounts and choose how
            you want to trade.
          </p>
        </div>
        <a
          href={OFFICIAL_LINKS.github}
          target="_blank"
          rel="noopener noreferrer"
        >
          Installation guide <ArrowUpRight size={18} />
        </a>
      </section>
    </div>
  )
}
