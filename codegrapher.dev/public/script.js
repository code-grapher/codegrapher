// CodeGrapher landing — copy-to-clipboard buttons only. No dependencies.
(function () {
  "use strict";

  function flash(btn) {
    var label = btn.querySelector(".copy-label");
    var original = label ? label.textContent : null;
    btn.classList.add("copied");
    if (label) label.textContent = "Copied";
    window.setTimeout(function () {
      btn.classList.remove("copied");
      if (label && original !== null) label.textContent = original;
    }, 1400);
  }

  function textFor(targetId) {
    var el = document.getElementById(targetId);
    if (!el) return "";
    // innerText collapses the syntax-highlighting <span>s back to plain text.
    return (el.innerText || el.textContent || "").trim();
  }

  document.querySelectorAll(".copy-btn").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var text = textFor(btn.getAttribute("data-copy-target"));
      if (!text) return;

      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(function () { flash(btn); });
      } else {
        // Fallback for non-secure contexts.
        var ta = document.createElement("textarea");
        ta.value = text;
        ta.setAttribute("readonly", "");
        ta.style.position = "absolute";
        ta.style.left = "-9999px";
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand("copy"); flash(btn); } catch (e) { /* no-op */ }
        document.body.removeChild(ta);
      }
    });
  });
})();
