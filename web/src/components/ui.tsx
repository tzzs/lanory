import * as React from 'react'
import { cn } from '@/lib/cn'

/* -------------------------------------------------------------------------- */
/* Button                                                                     */
/* -------------------------------------------------------------------------- */

type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger'
type ButtonSize = 'sm' | 'md' | 'icon'

const buttonVariants: Record<ButtonVariant, string> = {
  primary:
    'bg-accent text-white hover:opacity-90 active:opacity-80 disabled:opacity-50',
  secondary:
    'bg-surface-2 text-fg border border-border hover:bg-border/60 disabled:opacity-50',
  ghost: 'text-muted hover:text-fg hover:bg-surface-2 disabled:opacity-50',
  danger: 'bg-danger text-white hover:opacity-90 active:opacity-80 disabled:opacity-50',
}

const buttonSizes: Record<ButtonSize, string> = {
  sm: 'h-7 px-2.5 text-xs gap-1.5',
  md: 'h-9 px-3.5 text-sm gap-2',
  icon: 'h-8 w-8',
}

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant
  size?: ButtonSize
}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = 'secondary', size = 'md', type = 'button', ...props }, ref) => (
    <button
      ref={ref}
      type={type}
      className={cn(
        'inline-flex items-center justify-center rounded-md font-medium transition-[opacity,background-color,color] duration-150',
        'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/60 focus-visible:ring-offset-1 focus-visible:ring-offset-bg',
        'disabled:cursor-not-allowed',
        buttonVariants[variant],
        buttonSizes[size],
        className,
      )}
      {...props}
    />
  ),
)
Button.displayName = 'Button'

/* -------------------------------------------------------------------------- */
/* Card                                                                       */
/* -------------------------------------------------------------------------- */

export const Card = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => (
    <div
      ref={ref}
      className={cn('rounded-lg border border-border bg-surface', className)}
      {...props}
    />
  ),
)
Card.displayName = 'Card'

export function CardHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('flex items-center justify-between px-4 py-3 border-b border-border', className)} {...props} />
}

export function CardTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <h3 className={cn('text-sm font-semibold text-fg', className)} {...props} />
}

export function CardBody({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('p-4', className)} {...props} />
}

/* -------------------------------------------------------------------------- */
/* Page / section headings                                                    */
/* -------------------------------------------------------------------------- */

/**
 * The single page-level title, with an optional description and actions slot.
 *
 * It renders `h1`, not `h2`: before this component the document had no `h1` at
 * all, so a screen reader's heading outline started at level two and "go to the
 * page heading" went nowhere. Sub-sections use `SectionHeader` (`h2`), which
 * keeps the levels ordered instead of jumping from one to three.
 */
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3">
      <div className="min-w-0">
        <h1 className="text-lg font-semibold text-fg">{title}</h1>
        {description ? <p className="text-xs text-muted">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </div>
  )
}

/**
 * A sub-section of a page: `h2` under `PageHeader`'s `h1`. The typography is
 * the same as `CardTitle`, so a section reads the same whether or not it sits
 * inside a card; the spacing above/below stays with the call site, because a
 * section in a grid and a section above a table need different rhythm.
 *
 * `w-full` is deliberate: the element is also used as the sole child of
 * `CardHeader`, which is a flex row, and without it the heading would shrink to
 * its text and the actions slot would stop hugging the right edge.
 */
export function SectionHeader({
  title,
  description,
  actions,
  className,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex w-full flex-wrap items-center justify-between gap-3', className)}>
      <div className="min-w-0">
        <h2 className="text-sm font-semibold text-fg">{title}</h2>
        {description ? <p className="mt-0.5 text-[11px] text-muted">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
    </div>
  )
}

/* -------------------------------------------------------------------------- */
/* Badge                                                                      */
/* -------------------------------------------------------------------------- */

type BadgeTone = 'neutral' | 'success' | 'warning' | 'danger' | 'accent'

const badgeTones: Record<BadgeTone, string> = {
  neutral: 'bg-surface-2 text-muted border-border',
  success: 'bg-success/12 text-success border-success/25',
  warning: 'bg-warning/12 text-warning border-warning/25',
  danger: 'bg-danger/12 text-danger border-danger/25',
  accent: 'bg-accent/12 text-accent border-accent/25',
}

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  tone?: BadgeTone
}

export function Badge({ className, tone = 'neutral', ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium leading-none',
        badgeTones[tone],
        className,
      )}
      {...props}
    />
  )
}

/* -------------------------------------------------------------------------- */
/* Status dot                                                                 */
/* -------------------------------------------------------------------------- */

export interface StatusDotProps extends React.HTMLAttributes<HTMLSpanElement> {
  state: 'online' | 'offline' | 'unknown' | 'sleeping'
}

const dotColors: Record<StatusDotProps['state'], string> = {
  online: 'bg-success',
  offline: 'bg-danger',
  sleeping: 'bg-warning',
  unknown: 'bg-muted',
}

/**
 * A state dot. The state is also conveyed by a title attribute and, in the
 * surrounding markup, by text, so colour is never the only signal.
 */
export function StatusDot({ state, className, ...props }: StatusDotProps) {
  return (
    <span
      className={cn('inline-block h-2 w-2 shrink-0 rounded-full', dotColors[state], className)}
      title={state}
      aria-label={state}
      role="img"
      {...props}
    />
  )
}

/* -------------------------------------------------------------------------- */
/* Input / Select / Checkbox                                                  */
/* -------------------------------------------------------------------------- */

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  invalid?: boolean
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, invalid, ...props }, ref) => (
    <input
      ref={ref}
      aria-invalid={invalid || undefined}
      className={cn(
        'h-9 w-full rounded-md border border-border bg-surface px-3 text-sm text-fg placeholder:text-muted',
        'transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:border-accent',
        'disabled:cursor-not-allowed disabled:opacity-60',
        invalid && 'border-danger focus-visible:ring-danger/40',
        className,
      )}
      {...props}
    />
  ),
)
Input.displayName = 'Input'

/**
 * A field label. Stacked above its control rather than set inline, so inputs in
 * the same column start at the same x no matter how long their labels are.
 * Pass `htmlFor` to tie it to the control's `id`; a label that is not
 * associated with a control gives that control no accessible name.
 */
export function Label({ className, children, ...props }: React.LabelHTMLAttributes<HTMLLabelElement>) {
  return (
    <label className={cn('mb-1.5 block text-xs text-muted', className)} {...props}>
      {children}
    </label>
  )
}

// Form field wrapper with consistent spacing between label and input
export function Field({
  label,
  description,
  children,
  className,
}: {
  label?: React.ReactNode
  description?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <div className={className}>
      {label ? <Label>{label}</Label> : null}
      {children}
      {description ? (
        <p className="mt-1.5 text-[11px] text-muted">{description}</p>
      ) : null}
    </div>
  )
}


export const Select = React.forwardRef<HTMLSelectElement, React.SelectHTMLAttributes<HTMLSelectElement>>(
  ({ className, ...props }, ref) => (
    <select
      ref={ref}
      className={cn(
        'h-9 w-full rounded-md border border-border bg-surface px-2.5 text-sm text-fg',
        'transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:border-accent',
        'disabled:cursor-not-allowed disabled:opacity-60',
        className,
      )}
      {...props}
    />
  ),
)
Select.displayName = 'Select'

export interface CheckboxProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: React.ReactNode
}

export function Checkbox({ className, label, id, ...props }: CheckboxProps) {
  const generated = React.useId()
  const inputId = id ?? generated
  return (
    <div className="flex items-center gap-2.5">
      <input
        id={inputId}
        type="checkbox"
        className={cn(
          'h-4 w-4 rounded border-border bg-surface text-accent',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50',
          className,
        )}
        {...props}
      />
      {label ? (
        <label htmlFor={inputId} className="text-sm text-fg select-none cursor-pointer leading-relaxed">
          {label}
        </label>
      ) : null}
    </div>
  )
}

/* -------------------------------------------------------------------------- */
/* Table                                                                      */
/* -------------------------------------------------------------------------- */

export function Table({ className, ...props }: React.TableHTMLAttributes<HTMLTableElement>) {
  return (
    <div className="w-full overflow-x-auto">
      <table className={cn('w-full border-collapse text-sm', className)} {...props} />
    </div>
  )
}

export function THead({ className, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) {
  return <thead className={cn('border-b border-border text-left text-xs text-muted', className)} {...props} />
}

export function TBody({ className, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody className={cn('divide-y divide-border', className)} {...props} />
}

export function TR({ className, ...props }: React.HTMLAttributes<HTMLTableRowElement>) {
  return <tr className={cn('transition-colors hover:bg-surface-2/60', className)} {...props} />
}

export function TH({ className, ...props }: React.ThHTMLAttributes<HTMLTableCellElement>) {
  return <th className={cn('px-3 py-2 font-medium whitespace-nowrap', className)} {...props} />
}

export function TD({ className, ...props }: React.TdHTMLAttributes<HTMLTableCellElement>) {
  return <td className={cn('px-3 py-2.5 align-middle text-fg', className)} {...props} />
}

/* -------------------------------------------------------------------------- */
/* Skeleton / empty / error states                                            */
/* -------------------------------------------------------------------------- */

export function Skeleton({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('animate-pulse rounded bg-surface-2', className)} {...props} />
}

export function EmptyState({
  title,
  description,
  action,
  className,
}: {
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center gap-2 px-6 py-12 text-center', className)}>
      <p className="text-sm font-medium text-fg">{title}</p>
      {description ? <p className="max-w-sm text-xs text-muted">{description}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  )
}

export function ErrorState({
  title,
  description,
  action,
  className,
}: {
  title: string
  description?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      role="alert"
      className={cn('flex flex-col items-center justify-center gap-2 px-6 py-12 text-center', className)}
    >
      <p className="text-sm font-medium text-danger">{title}</p>
      {description ? <p className="max-w-md text-xs text-muted">{description}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  )
}

/* -------------------------------------------------------------------------- */
/* Modal                                                                      */
/* -------------------------------------------------------------------------- */

export interface ModalProps {
  open: boolean
  onClose: () => void
  title: string
  description?: string
  children?: React.ReactNode
  footer?: React.ReactNode
  width?: 'sm' | 'md' | 'lg'
}

const modalWidths = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-2xl' }

const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ')

/**
 * Focusable descendants of `root`, in document order.
 *
 * Elements with no layout box are dropped: `offsetParent` is null inside a
 * `display: none` subtree, and this app hides controls with utility classes, so
 * a naive query would let Tab land on something the user cannot see.
 */
function focusableIn(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>(focusableSelector)).filter(
    (el) => el.offsetParent !== null,
  )
}

export function Modal({ open, onClose, title, description, children, footer, width = 'md' }: ModalProps) {
  const dialogRef = React.useRef<HTMLDivElement>(null)
  const onCloseRef = React.useRef(onClose)

  // Held in a ref so a parent that re-creates the callback on every render does
  // not re-run the effects below and yank focus back to the first control.
  React.useEffect(() => {
    onCloseRef.current = onClose
  })

  React.useEffect(() => {
    if (!open) return
    const dialog = dialogRef.current
    if (!dialog) return

    // This dialog is hand-rolled, so this trap is what makes aria-modal
    // truthful: without it Tab walks out of the dialog and into the page
    // behind the backdrop, where the user cannot see where focus went.
    const restoreTo = document.activeElement instanceof HTMLElement ? document.activeElement : null

    const initial = focusableIn(dialog)
    if (initial.length > 0) initial[0].focus()
    else dialog.focus()

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onCloseRef.current()
        return
      }
      if (e.key !== 'Tab') return

      // Re-query on every Tab: content inside the dialog can grow or shrink
      // (a form gains fields, a submit button becomes disabled while pending).
      const items = focusableIn(dialog)
      if (items.length === 0) {
        e.preventDefault()
        dialog.focus()
        return
      }

      const first = items[0]
      const last = items[items.length - 1]
      const active = document.activeElement
      const escaped = !dialog.contains(active)

      if (e.shiftKey) {
        if (escaped || active === first || active === dialog) {
          e.preventDefault()
          last.focus()
        }
      } else if (escaped || active === last || active === dialog) {
        e.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('keydown', onKey)
      if (restoreTo && restoreTo.isConnected) restoreTo.focus()
    }
  }, [open])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div
        className="absolute inset-0 bg-black/40 animate-fade-in"
        onClick={onClose}
        aria-hidden="true"
      />
      <Card
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className={cn('relative z-10 w-full animate-slide-up shadow-xl', modalWidths[width])}
      >
        <CardHeader>
          <div>
            <CardTitle>{title}</CardTitle>
            {description ? <p className="mt-0.5 text-xs text-muted">{description}</p> : null}
          </div>
          <Button variant="ghost" size="sm" onClick={onClose} aria-label="close">
            ✕
          </Button>
        </CardHeader>
        <CardBody>{children}</CardBody>
        {footer ? (
          <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3">{footer}</div>
        ) : null}
      </Card>
    </div>
  )
}

/* -------------------------------------------------------------------------- */
/* Key/value row (used in detail panels)                                      */
/* -------------------------------------------------------------------------- */

export function FieldRow({
  label,
  children,
  mono,
  className,
}: {
  label: React.ReactNode
  children: React.ReactNode
  mono?: boolean
  className?: string
}) {
  return (
    <div className={cn('flex items-baseline justify-between gap-4 py-1.5', className)}>
      <dt className="shrink-0 text-xs text-muted">{label}</dt>
      <dd className={cn('min-w-0 text-right text-sm text-fg break-all', mono && 'font-mono')}>{children}</dd>
    </div>
  )
}
