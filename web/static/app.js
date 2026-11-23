// Ask before htmx issues a destructive request. Elements opt in with
// data-confirm="..." so the prompt can be styled or localised later.
document.addEventListener("htmx:confirm", (event) => {
  const message = event.detail.elt.dataset.confirm;
  if (!message) {
    return;
  }
  event.preventDefault();
  if (window.confirm(message)) {
    event.detail.issueRequest(true);
  }
});
