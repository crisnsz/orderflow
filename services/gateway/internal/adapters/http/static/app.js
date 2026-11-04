"use strict";

const SHORT_ORDER_ID_LENGTH = 8;
const CENTS_PER_EURO = 100;

const byId = (id) => document.getElementById(id);
let orderEventSource = null;

async function fetchJson(path, requestOptions = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: requestOptions.body ? { "Content-Type": "application/json" } : {},
    ...requestOptions,
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || response.statusText);
  return body;
}

const formatEuros = (cents) => (cents / CENTS_PER_EURO).toFixed(2) + " EUR";
const shortOrderId = (orderId) => orderId.slice(0, SHORT_ORDER_ID_LENGTH);

const makeElement = (tagName, text, className) => {
  const element = document.createElement(tagName);
  if (text !== undefined) element.textContent = text;
  if (className) element.className = className;
  return element;
};

async function loadUsers() {
  const users = await fetchJson("/api/users");
  const usersBox = byId("users");
  usersBox.replaceChildren();
  for (const user of users) {
    const userCard = makeElement("div", undefined, "card");
    userCard.append(makeElement("strong", user.name), makeElement("p", user.note, "hint"));
    const loginButton = makeElement("button", "Log in as " + user.name);
    loginButton.onclick = () => login(user.id);
    userCard.append(loginButton);
    usersBox.append(userCard);
  }
}

async function login(userId) {
  await fetchJson("/login", { method: "POST", body: JSON.stringify({ user_id: userId }) });
  await showSignedInView();
}

function showLoginView() {
  byId("login").hidden = false;
  byId("app").hidden = true;
  byId("whoami").hidden = true;
}

async function showSignedInView() {
  let profile;
  try {
    profile = await fetchJson("/api/me");
  } catch {
    showLoginView();
    return;
  }
  byId("login").hidden = true;
  byId("app").hidden = false;
  byId("whoami").hidden = false;
  byId("who").textContent = "Signed in as " + profile.user.name;
  renderCards(profile.cards);
  await Promise.all([loadProducts(), loadFailureStage(), loadOrders()]);
}

function renderCards(cards) {
  const cardsBox = byId("cards");
  cardsBox.replaceChildren();
  if (!cards.length) cardsBox.append(makeElement("p", "No saved card.", "hint"));
  for (const card of cards) {
    const cardBox = makeElement("div", undefined, "card");
    cardBox.append(
      makeElement("strong", `${card.brand} ending ${card.last4} `),
      makeElement("span", `exp ${card.exp_month}/${card.exp_year} `, "hint"),
    );
    cardBox.append(
      makeElement("span", card.autopay_consent ? "autopay ON" : "autopay OFF", "badge " + (card.autopay_consent ? "on" : "off")),
    );
    cardsBox.append(cardBox);
  }
}

function createQuantityInput(product) {
  const quantityInput = makeElement("input");
  quantityInput.type = "number";
  quantityInput.min = "0";
  quantityInput.value = "0";
  quantityInput.dataset.sku = product.sku;
  quantityInput.setAttribute("aria-label", "Quantity of " + product.name);
  return quantityInput;
}

async function loadProducts() {
  const products = await fetchJson("/api/products");
  const productsBody = document.querySelector("#products tbody");
  productsBody.replaceChildren();
  for (const product of products) {
    const row = makeElement("tr");
    const quantityCell = makeElement("td");
    quantityCell.append(createQuantityInput(product));
    row.append(
      makeElement("td", `${product.name} (${product.sku})`),
      makeElement("td", formatEuros(product.price_cents)),
      makeElement("td", String(product.stock)),
      quantityCell,
    );
    productsBody.append(row);
  }
}

async function loadFailureStage() {
  const { stage } = await fetchJson("/api/admin/failure");
  byId("stage").value = stage;
}

async function loadOrders() {
  const orders = await fetchJson("/api/orders");
  const ordersList = byId("orders");
  ordersList.replaceChildren();
  for (const order of orders) {
    const listItem = makeElement("li");
    const orderLink = makeElement("a", shortOrderId(order.id));
    orderLink.onclick = () => followOrder(order);
    listItem.append(orderLink, makeElement("span", `  ${order.state}  ${formatEuros(order.total_cents)}`, "hint"));
    ordersList.append(listItem);
  }
}

function readRequestedItems() {
  return [...document.querySelectorAll("#products input")]
    .map((quantityInput) => ({ sku: quantityInput.dataset.sku, quantity: parseInt(quantityInput.value, 10) || 0 }))
    .filter((item) => item.quantity > 0);
}

byId("place").onclick = async () => {
  const items = readRequestedItems();
  if (!items.length) {
    byId("place-msg").textContent = "Choose at least one product.";
    return;
  }
  byId("place-msg").textContent = "Placing...";
  try {
    const order = await fetchJson("/api/orders", { method: "POST", body: JSON.stringify({ items }) });
    byId("place-msg").textContent = "Order " + shortOrderId(order.id) + " is " + order.state + (order.cancel_reason ? ": " + order.cancel_reason : "");
    followOrder(order);
    loadProducts();
    loadOrders();
  } catch (error) {
    byId("place-msg").textContent = "Error: " + error.message;
  }
};

byId("apply-stage").onclick = async () => {
  try {
    await fetchJson("/api/admin/failure", { method: "PUT", body: JSON.stringify({ stage: byId("stage").value }) });
    byId("stage-msg").textContent = "Applied.";
  } catch (error) {
    byId("stage-msg").textContent = "Error: " + error.message;
  }
};

byId("logout").onclick = async () => {
  if (orderEventSource) orderEventSource.close();
  await fetchJson("/logout", { method: "POST" });
  await showSignedInView();
};

const EVENT_LABELS = {
  "order.placed": ["Order placed", ""],
  "order.confirmed": ["Stock reserved, order confirmed", ""],
  "invoice.generated": ["Invoice generated", ""],
  "payment.succeeded": ["Payment succeeded", "good"],
  "payment.failed": ["Payment failed", "bad"],
  "payment.pending_manual": ["Waiting for manual payment (no autopay consent)", "warn"],
  "order.cancelled": ["Order cancelled", "bad"],
  "order.completed": ["Order completed", "good"],
};

const STATE_AFTER_EVENT = {
  "order.confirmed": "INVENTORY_RESERVED",
  "invoice.generated": "INVOICED",
  "payment.succeeded": "PAID",
  "payment.failed": "COMPENSATING",
  "order.completed": "COMPLETED",
  "order.cancelled": "CANCELLED",
};

const STATE_PROGRESS = {
  PENDING: 0,
  INVENTORY_RESERVED: 1,
  INVOICED: 2,
  PAID: 3,
  COMPENSATING: 4,
  COMPLETED: 5,
  CANCELLED: 5,
};

function describeEventData(event) {
  const data = event.data || {};
  const parts = [];
  if (data.invoice_id) parts.push("invoice " + data.invoice_id);
  if (data.amount_cents) parts.push(formatEuros(data.amount_cents));
  if (data.reference) parts.push("ref " + data.reference);
  if (data.reason) parts.push("reason: " + data.reason);
  return parts.join(" | ");
}

function appendTimelineEntry(event) {
  const [label, cssClass] = EVENT_LABELS[event.type] || [event.type, ""];
  const entry = makeElement("li", undefined, cssClass);
  entry.append(makeElement("div", label), makeElement("span", event.type, "ev-type"));
  const detail = describeEventData(event);
  if (detail) entry.append(makeElement("div", detail, "ev-detail"));
  entry.append(makeElement("time", new Date(event.at).toLocaleTimeString()));
  byId("timeline").append(entry);
}

function addEvent(event) {
  appendTimelineEntry(event);
  const newState = STATE_AFTER_EVENT[event.type];
  if (newState && STATE_PROGRESS[newState] > STATE_PROGRESS[byId("state").textContent]) showState(newState);
}

function showState(state) {
  const stateChip = byId("state");
  stateChip.hidden = false;
  stateChip.textContent = state;
  stateChip.className = "chip " + state;
}

function followOrder(order) {
  if (orderEventSource) orderEventSource.close();
  byId("timeline").replaceChildren();
  byId("order-line").textContent = "Order " + order.id + " (" + formatEuros(order.total_cents) + ")";
  showState(order.state);
  const shownEventIds = new Set();
  orderEventSource = new EventSource(`/api/orders/${order.id}/events`);
  for (const eventType of Object.keys(EVENT_LABELS)) {
    orderEventSource.addEventListener(eventType, (messageEvent) => {
      const event = JSON.parse(messageEvent.data);
      if (shownEventIds.has(event.id)) return;
      shownEventIds.add(event.id);
      addEvent(event);
      if (eventType === "order.completed" || eventType === "order.cancelled") {
        loadOrders();
        loadProducts();
      }
    });
  }
}

loadUsers().then(showSignedInView);
