/**
 * Greet someone by name
 * @param {string} name - The name to greet
 * @returns {string} The greeting message
 */
function greet(name) {
  return `Hello, ${name}!`;
}

/**
 * Add two numbers
 * @param {number} a - First number
 * @param {number} b - Second number
 * @returns {number} The sum
 */
function add(a, b) {
  return a + b;
}

module.exports = { greet, add };

