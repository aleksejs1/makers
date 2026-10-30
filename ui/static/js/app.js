// Small progressive enhancements. The site works without JavaScript.
(function () {
  "use strict";

  // Tag suggestion chips append their tag to the related input.
  document.addEventListener("click", function (e) {
    var chip = e.target.closest("[data-tag]");
    if (!chip) return;
    var box = chip.closest("[data-tags-for]");
    var input = box && document.getElementById(box.getAttribute("data-tags-for"));
    if (!input) return;
    var tag = chip.getAttribute("data-tag");
    var tags = input.value.split(",").map(function (t) { return t.trim(); }).filter(Boolean);
    var exists = tags.some(function (t) { return t.toLowerCase() === tag.toLowerCase(); });
    if (!exists) tags.push(tag);
    input.value = tags.join(", ");
    chip.hidden = true;
    input.focus();
  });

  // Close the account menu when clicking elsewhere.
  document.addEventListener("click", function (e) {
    document.querySelectorAll("details.menu[open]").forEach(function (d) {
      if (!d.contains(e.target)) d.removeAttribute("open");
    });
  });
})();
