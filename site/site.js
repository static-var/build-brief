/* build-brief  --  "Signal" site interactions */
(function () {
  "use strict";

  /* ---- Mobile nav toggle ---------------------------------- */
  const toggle = document.querySelector(".topbar__toggle");
  const topbar = document.querySelector(".topbar");
  if (toggle && topbar) {
    toggle.addEventListener("click", () =>
      topbar.classList.toggle("topbar--open")
    );
  }

  /* ---- Install tabs --------------------------------------- */
  document.querySelectorAll(".install-tab").forEach((tab) => {
    tab.addEventListener("click", () => {
      const group = tab.closest(".install-section");
      group
        .querySelectorAll(".install-tab")
        .forEach((t) => t.setAttribute("aria-selected", "false"));
      group
        .querySelectorAll(".install-panel")
        .forEach((p) => p.removeAttribute("data-active"));
      tab.setAttribute("aria-selected", "true");
      const target = group.querySelector(
        `.install-panel[data-panel="${tab.dataset.target}"]`
      );
      if (target) target.setAttribute("data-active", "");
    });
  });

  /* ---- Scroll fade-in ------------------------------------- */
  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((e) => {
        if (e.isIntersecting) {
          e.target.classList.add("visible");
          observer.unobserve(e.target);
        }
      });
    },
    { threshold: 0.12 }
  );
  document.querySelectorAll(".fade-in").forEach((el) => observer.observe(el));

  /* ---- Animate gain bars on scroll ------------------------ */
  const barObserver = new IntersectionObserver(
    (entries) => {
      entries.forEach((e) => {
        if (e.isIntersecting) {
          e.target.querySelectorAll(".gains-bar__fill").forEach((fill) => {
            fill.style.width = fill.dataset.width;
          });
          barObserver.unobserve(e.target);
        }
      });
    },
    { threshold: 0.3 }
  );
  document
    .querySelectorAll(".gains-bar")
    .forEach((el) => barObserver.observe(el));

  /* ---- Copy install snippet ------------------------------- */
  document.querySelectorAll("[data-copy]").forEach((btn) => {
    btn.addEventListener("click", () => {
      const text = btn.dataset.copy;
      navigator.clipboard.writeText(text).then(() => {
        const orig = btn.textContent;
        btn.textContent = "copied";
        setTimeout(() => (btn.textContent = orig), 1200);
      });
    });
  });
})();
