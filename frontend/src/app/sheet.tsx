// Bounded host sheet/overlay on the NATIVE <dialog> element: showModal()
// places it in the top layer and makes the rest of the document inert —
// including the reader iframe — without moving content in the React tree,
// so one live viewer/channel and every evidence stream stay mounted. Native
// dialog semantics provide Tab/Shift+Tab containment, Escape (cancel) and
// focus return; when the opening trigger became hidden (breakpoint change),
// focus falls back to the panel's header toggle instead of the body.
import { useEffect, useRef } from "react";

export interface SheetProps {
  /** Whether the sheet is currently open/visible to the user. */
  open: boolean;
  /** Modal mode: showModal (top layer + background inert). Non-modal keeps
   *  the panel an ordinary open column (the declared wide layout). */
  modal: boolean;
  /** Accessible name for the dialog region. */
  label: string;
  /** Stable id so header toggles can reference the panel via aria-controls. */
  id?: string;
  /** Header toggle to focus when a close cannot return to its (now hidden) opener. */
  openerId: string;
  /** Persistent Files columns stay visible; dismissible Context also closes
   *  with Escape in its non-modal wide mode. */
  closeOnEscape?: boolean;
  onClose(): void;
  className?: string;
  children: React.ReactNode;
}

export function Sheet(props: SheetProps) {
  const { open, modal, label, onClose } = props;
  const dialog = useRef<HTMLDialogElement | null>(null);
  const applied = useRef<{ open: boolean; modal: boolean }>({ open: false, modal: false });

  useEffect(() => {
    const node = dialog.current;
    if (node === null) return;
    const previous = applied.current;
    if (open && previous.open && previous.modal !== modal) {
      // Mode transition while open (breakpoint change): a bare open attribute
      // cannot leave/enter the top layer, so perform the REAL close + reopen
      // in the new mode on the SAME element — content and the live viewer
      // behind it are never remounted, background inertness is reconciled,
      // and focus inside the sheet is preserved across the transition.
      const restore = document.activeElement instanceof HTMLElement && node.contains(document.activeElement) ? document.activeElement : null;
      node.close();
      if (modal) node.showModal();
      else node.show();
      if (restore !== null && node.contains(restore) && document.activeElement !== restore) restore.focus({ preventScroll: true });
    } else if (open) {
      if (modal) { if (!node.open) node.showModal(); }
      else if (!node.open) node.show();
    } else if (node.open) {
      node.close();
    }
    applied.current = { open, modal };
  }, [open, modal]);

  useEffect(() => {
    const node = dialog.current;
    if (node === null) return;
    const onCancel = (event: Event) => { event.preventDefault(); onClose(); };
    const onKeyDown = (event: KeyboardEvent) => {
      if (node.matches(":modal") && event.key === "Tab" && !event.altKey && !event.ctrlKey && !event.metaKey) {
        const stops = [...node.querySelectorAll<HTMLElement>("button,input,select,textarea,a[href],summary,[tabindex]")]
          .filter((element) => element.tabIndex >= 0 && !element.matches(":disabled") && element.getClientRects().length > 0);
        const first = stops[0], last = stops[stops.length - 1];
        if (!first || !last) {
          event.preventDefault();
          node.focus();
        } else if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
      if (props.closeOnEscape !== false && event.key === "Escape" && !event.defaultPrevented && !node.matches(":modal")) {
        event.preventDefault();
        onClose();
      }
    };
    // Native focus return runs first; when the opener is no longer
    // focusable (hidden at the new breakpoint) the body would keep focus —
    // route it to this panel's visible header toggle instead.
    const onClosed = () => {
      if (document.activeElement === document.body || document.activeElement === null) {
        document.getElementById(props.openerId)?.focus({ preventScroll: true });
      }
    };
    node.addEventListener("cancel", onCancel);
    node.addEventListener("close", onClosed);
    node.addEventListener("keydown", onKeyDown);
    return () => {
      node.removeEventListener("cancel", onCancel);
      node.removeEventListener("close", onClosed);
      node.removeEventListener("keydown", onKeyDown);
    };
  }, [onClose, props.openerId, props.closeOnEscape]);

  return (
    <dialog
      ref={dialog}
      id={props.id}
      className={props.className}
      aria-label={label}
    >
      {props.children}
    </dialog>
  );
}
