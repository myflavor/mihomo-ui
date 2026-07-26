/**
 * Closing a modal by clicking its backdrop, without the drag-out trap.
 *
 * `@click.self` alone is wrong: a click fires on the nearest common ancestor of
 * mousedown and mouseup, so selecting text inside the dialog and releasing over
 * the backdrop reports the backdrop as the target — and the dialog vanishes
 * mid-selection. Requiring the press to have started on the backdrop as well
 * makes the gesture mean what it looks like.
 */
export function useMaskClose(onClose) {
  let pressedOnMask = false

  return {
    onMousedown(e) {
      pressedOnMask = e.target === e.currentTarget
    },
    onClick(e) {
      const wasOnMask = pressedOnMask
      pressedOnMask = false
      if (wasOnMask && e.target === e.currentTarget) onClose()
    },
  }
}
